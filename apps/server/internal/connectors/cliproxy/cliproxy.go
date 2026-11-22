// Package cliproxy is the CLIProxyAPI model connector (connectors/src/cliproxy)
// and the gateway runtime it shares with the generic model gateway: per role, the anthropic
// protocol uses /v1/messages and openai uses /v1/chat/completions (PLAN 5.2),
// on the M2 provider clients.
package cliproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// Roles are the model roles a gateway may serve, in order.
var Roles = []string{"default", "fast", "vision"}

// GatewayConfigZod is a gateway manifest's config with its default base URL.
func GatewayConfigZod(defaultBaseURL string) *zod.ObjectS {
	proto := zod.Default(zod.Enum("anthropic", "openai"), "anthropic")
	return zod.Object(
		zod.F("baseUrl", zod.Default(zod.String().URL(nil), defaultBaseURL)),
		zod.F("protocols", zod.Default(zod.Object(zod.F("default", proto), zod.F("fast", proto), zod.F("vision", proto)).Partial(), jsonx.NewMap[any]())),
	)
}

// RequiredGatewayConfigZod is a gateway config with no default base URL, for
// a gateway the owner points at themselves.
func RequiredGatewayConfigZod() *zod.ObjectS {
	proto := zod.Default(zod.Enum("anthropic", "openai"), "anthropic")
	return zod.Object(
		zod.F("baseUrl", zod.String().URL(nil)),
		zod.F("protocols", zod.Default(zod.Object(zod.F("default", proto), zod.F("fast", proto), zod.F("vision", proto)).Partial(), jsonx.NewMap[any]())),
	)
}

var SecretZod = []zod.Field{zod.F("apiKey", zod.String().Min(1))}

var Manifest = connectors.Manifest{
	ID:      "cliproxy",
	Kind:    "model",
	Name:    "CLIProxyAPI",
	Icon:    "cpu",
	Summary: "Claude and Gemini through CLIProxyAPI on the host.",
	Setup: []connectors.SetupStep{
		{Field: "baseUrl", Label: "Base URL", Placeholder: "http://host.docker.internal:8317", Optional: true},
		{Field: "apiKey", Label: "API key", Secret: true, Help: "Run this on the CLIProxyAPI Mac, then paste a key.", Command: "grep -A3 api-keys $(brew --prefix)/etc/cliproxyapi.conf"},
	},
	Docs:         &connectors.Docs{Commands: []string{"/connectors on cliproxy", "/connectors off cliproxy"}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    GatewayConfigZod("http://host.docker.internal:8317"),
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(GatewayConfigZod("http://host.docker.internal:8317")),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

// GatewayConfig is the parsed config.
type GatewayConfig struct {
	BaseURL   string
	Protocols map[string]string
	// The protocols object as stored (for live()).
	ProtocolsRaw *jsonx.Object
}

// ParseGatewayConfig is manifest.config.parse(raw ?? {}).
func ParseGatewayConfig(m connectors.Manifest, raw *jsonx.Object) (GatewayConfig, error) {
	if raw == nil {
		raw = jsonx.NewMap[any]()
	}
	out, err := m.ParseConfig(raw)
	if err != nil {
		return GatewayConfig{}, err
	}
	cfg := GatewayConfig{Protocols: map[string]string{}, ProtocolsRaw: jsonx.NewMap[any]()}
	if v, ok := out.Get("baseUrl"); ok {
		cfg.BaseURL, _ = v.(string)
	}
	if v, ok := out.Get("protocols"); ok {
		if p, ok := v.(*jsonx.Object); ok {
			cfg.ProtocolsRaw = p
			for _, k := range p.Keys() {
				x, _ := p.Get(k)
				cfg.Protocols[k], _ = x.(string)
			}
		}
	}
	return cfg, nil
}

var trailingSlashes = regexp.MustCompile(`/+$`)

func base(cfg GatewayConfig) string { return trailingSlashes.ReplaceAllString(cfg.BaseURL, "") }

// BuildModels is buildModels: a model per id and role, with the role's protocol.
func BuildModels(name string, cfg GatewayConfig, apiKey string, hc *http.Client) func(modelID, role string) (models.LanguageModel, error) {
	b := base(cfg) + "/v1"
	return func(modelID, role string) (models.LanguageModel, error) {
		if cfg.Protocols[role] == "openai" {
			return models.NewOpenAI(models.OpenAIConfig{BaseURL: b, APIKey: apiKey, Name: name, Client: hc}, modelID), nil
		}
		return models.NewAnthropic(models.AnthropicConfig{BaseURL: b, APIKey: apiKey, Client: hc}, modelID), nil
	}
}

// ListModels is listModels: GET /v1/models.
func ListModels(ctx context.Context, cfg GatewayConfig, apiKey string, hc *http.Client) ([]string, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base(cfg)+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("authorization", "Bearer "+apiKey)
		req.Header.Set("x-api-key", apiKey)
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("GET /v1/models answered %d", res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		out = append(out, m.ID)
	}
	return out, nil
}

func strList(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// GatewayRuntime is gatewayRuntime(manifest, {fetch}).
func GatewayRuntime(m connectors.Manifest, hc *http.Client) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: m,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			cfg, err := ParseGatewayConfig(m, c.Config)
			if err != nil {
				return connectors.TestResult{}, err
			}
			key := c.Secret("apiKey")
			ctx := c.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			list, err := ListModels(ctx, cfg, key, hc)
			if err != nil {
				return connectors.TestResult{OK: false, Detail: fmt.Sprintf("Can't reach %s: %s", cfg.BaseURL, err.Error())}, nil
			}
			chains, err := settings.Get[settings.ModelRoles](c.DB, "modelRoles")
			if err != nil {
				return connectors.TestResult{}, err
			}
			build := BuildModels(c.ID, cfg, key, hc)
			roles := jsonx.NewMap[any]()
			var parts []string
			ok := true
			for _, role := range Roles {
				var link *settings.ModelChainLink
				for _, l := range chains.Role(role) {
					if l.ConnectorID == c.ID {
						l := l
						link = &l
						break
					}
				}
				if link == nil {
					continue
				}
				lm, _ := build(link.Model, role)
				one := 1
				_, err := models.Generate(ctx, lm, models.CallOptions{Prompt: []models.Message{models.User("Reply with OK.")}, MaxOutputTokens: &one})
				if err != nil {
					ok = false
					v := link.Model + ": " + strx.Slice16(err.Error(), 120)
					roles.Set(role, v)
					parts = append(parts, role+" "+v)
				} else {
					v := link.Model + ": ok"
					roles.Set(role, v)
					parts = append(parts, role+" "+v)
				}
			}
			detail := fmt.Sprintf("%d models available", len(list))
			if keys := roles.Keys(); len(keys) > 0 {
				detail += ", " + strings.Join(keys, ", ") + " answered"
			}
			if !ok {
				detail = "Model call failed: " + strings.Join(parts, "; ")
			}
			return connectors.TestResult{OK: ok, Detail: detail, Data: kit.O("models", strList(list), "roles", roles)}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			cfg, err := ParseGatewayConfig(m, c.Config)
			if err != nil {
				return nil, err
			}
			key := c.Secret("apiKey")
			build := BuildModels(c.ID, cfg, key, hc)
			return &connectors.RunningConnector{
				Stop:  func(context.Context) error { return nil },
				Model: build,
				Live:  func() *jsonx.Object { return kit.O("baseUrl", cfg.BaseURL, "protocols", cfg.ProtocolsRaw) },
				Action: func(ctx context.Context, name string, input *jsonx.Object) (*jsonx.Object, error) {
					if name != "listModels" {
						return nil, &connectors.StatusError{Status: 404, Msg: "Unknown action " + name}
					}
					tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					list, err := ListModels(tctx, cfg, key, hc)
					if err != nil {
						return nil, err
					}
					return kit.O("models", strList(list)), nil
				},
			}, nil
		},
	}
}

// Runtime is the cliproxy connector runtime.
var Runtime = GatewayRuntime(Manifest, nil)
