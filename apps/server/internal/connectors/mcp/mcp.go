// Package mcp is the MCP server connector (connectors/src/mcp) and the MCP
// client the other connectors share.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var toolClass = zod.Enum("read", "write", "destructive")

// ToolPrefZod is mcpToolPref.
var ToolPrefZod = zod.Object(zod.F("enabled", zod.Bool()), zod.F("class", toolClass), zod.F("flowApproval", zod.Enum("version", "every_run").Optional()))

// ConfigZod is mcpConfig.
var ConfigZod = zod.Object(
	zod.F("url", zod.String().URL(regexp.MustCompile(`^https?$`))),
	zod.F("authMode", zod.Default(zod.Enum("none", "bearer", "header"), "bearer")),
	zod.F("headerName", zod.String().Trim().Min(1).Max(100).Optional()),
	zod.F("toolPrefs", zod.Default(zod.Record(ToolPrefZod), jsonx.NewMap[any]())),
	zod.F("addedFromChat", zod.Bool().Optional()),
	// Set by the owner on the agent's Tools tab; the runtime doesn't read it.
	zod.F(connectors.NoPrivateDataKey, zod.Bool().Optional()),
)

var SecretZod = []zod.Field{zod.F("token", zod.String().Min(1).Optional())}

var Manifest = connectors.Manifest{
	ID:       "mcp",
	Kind:     "tools",
	Name:     "MCP server",
	Icon:     "plug",
	Summary:  "Tools from an MCP server you trust.",
	Multiple: true,
	Trust:    "external",
	Setup: []connectors.SetupStep{
		{Field: "url", Label: "Server URL", Placeholder: "https://example.com/api/mcp", Help: "Streamable HTTP only, not SSE."},
		{Field: "authMode", Label: "Auth", AskFirst: true, Help: "bearer: Authorization header. header: a header you name."},
		{Field: "headerName", Label: "Header name", AskFirst: true, Placeholder: "x-api-key", ShowIf: &connectors.ShowIf{Field: "authMode", In: []string{"header"}}},
		{Field: "token", Label: "Token", Secret: true, Optional: true, AskFirst: true, ShowIf: &connectors.ShowIf{Field: "authMode", In: []string{"bearer", "header"}}},
		{Action: "listTools", Label: "Pick which tools Palui may use"},
	},
	Presets: []connectors.Preset{
		{ID: "linear", Label: "Linear", Config: kit.O("url", "https://mcp.linear.app/mcp", "authMode", "bearer"), SecretFields: []string{"token"}},
		{ID: "host", Label: "Host commands", Config: kit.O("url", "http://host.docker.internal:4893/mcp", "authMode", "bearer"), SecretFields: []string{"token"}, Trust: "own"},
	},
	RemoveWarning: "This removes the token from Palui. The server isn't touched.",
	Docs: &connectors.Docs{Notes: []string{
		"Tools are namespaced <connector id>__<tool>, for example mcp-linear__list_issues.",
		"Unknown or unclassified tools are treated as destructive and always ask first.",
	}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

// ToolPref is one saved per-tool switch.
type ToolPref struct {
	Enabled      bool
	Class        string
	FlowApproval string
}

// Config is the stored config as the runtime reads it (no defaults applied,
// like ctx.config in TS).
type Config struct {
	URL           string
	AuthMode      string
	HeaderName    string
	ToolPrefs     map[string]ToolPref
	AddedFromChat bool
}

func str(o *jsonx.Object, k string) string {
	if o == nil {
		return ""
	}
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

// ConfigFrom reads a stored config.
func ConfigFrom(o *jsonx.Object) Config {
	c := Config{URL: str(o, "url"), AuthMode: str(o, "authMode"), HeaderName: str(o, "headerName"), ToolPrefs: PrefsFrom(o)}
	if o != nil {
		v, _ := o.Get("addedFromChat")
		c.AddedFromChat = v == true
	}
	return c
}

// PrefsFrom reads config.toolPrefs.
func PrefsFrom(o *jsonx.Object) map[string]ToolPref {
	out := map[string]ToolPref{}
	if o == nil {
		return out
	}
	raw, _ := o.Get("toolPrefs")
	prefs, ok := raw.(*jsonx.Object)
	if !ok {
		return out
	}
	for _, k := range prefs.Keys() {
		v, _ := prefs.Get(k)
		p, ok := v.(*jsonx.Object)
		if !ok {
			continue
		}
		en, _ := p.Get("enabled")
		out[k] = ToolPref{Enabled: en == true, Class: str(p, "class"), FlowApproval: str(p, "flowApproval")}
	}
	return out
}

// Options are what tests inject: the HTTP client and the DNS resolver of the
// public-address guard.
type Options struct {
	Client   *http.Client
	Resolver safenet.Resolver
}

// EndpointFor is endpointFor. A server added from chat only ever talks to
// public addresses, checked on every request.
func EndpointFor(cfg Config, token string, opts Options) (Endpoint, error) {
	if cfg.AuthMode != "none" && token == "" {
		return Endpoint{}, errors.New("This server needs a token")
	}
	if cfg.AuthMode == "header" && cfg.HeaderName == "" {
		return Endpoint{}, errors.New("Set the header name for the custom header auth mode")
	}
	client := opts.Client
	if cfg.AddedFromChat {
		var base http.RoundTripper
		if client != nil {
			base = client.Transport
		}
		client = safenet.PublicOnlyClient(base, opts.Resolver)
	}
	return Endpoint{URL: cfg.URL, Headers: AuthHeaders(cfg.AuthMode, cfg.HeaderName, token), Client: client}, nil
}

// Listing is one row of the listTools wizard step / action.
func Listing(defs []ToolDef, prefs map[string]ToolPref) []any {
	out := make([]any, 0, len(defs))
	for _, d := range defs {
		suggested := SuggestClass(d.Annotations)
		pref, saved := prefs[d.Name]
		enabled, class, fa := true, suggested, "version"
		if saved {
			enabled = pref.Enabled
			if pref.Class != "" {
				class = pref.Class
			}
			if pref.FlowApproval != "" {
				fa = pref.FlowApproval
			}
		}
		o := kit.O("name", d.Name)
		if t := d.DisplayTitle(); t != "" {
			o.Set("title", t)
		}
		kit.Spread(o, kit.O("description", d.Description, "suggestedClass", suggested, "enabled", enabled, "class", class, "saved", saved, "flowApproval", fa))
		out = append(out, o)
	}
	return out
}

// Selected is a tool a turn may see.
type Selected struct {
	Def          ToolDef
	Class        string
	FlowApproval string
	// Set while the owner hasn't classified the tool (Class is then
	// destructive): the class its annotations suggest.
	Suggested string
}

// SelectTools: enabled ones only, with their saved class; a tool the owner
// hasn't classified yet is destructive (fail closed).
func SelectTools(defs []ToolDef, prefs map[string]ToolPref) []Selected {
	var out []Selected
	for _, d := range defs {
		pref, ok := prefs[d.Name]
		if ok && !pref.Enabled {
			continue
		}
		s := Selected{Def: d, Class: "destructive", FlowApproval: "version"}
		if ok && pref.FlowApproval != "" {
			s.FlowApproval = pref.FlowApproval
		}
		if ok && pref.Class != "" {
			s.Class = pref.Class
		} else {
			s.Suggested = SuggestClass(d.Annotations)
		}
		out = append(out, s)
	}
	return out
}

// Suggestion is one listTools row as WithSuggested reads it.
type Suggestion struct {
	Name  string
	Class string
}

// SuggestionsFrom reads the rows of a listTools answer. A class that isn't
// read or write is destructive.
func SuggestionsFrom(listed *jsonx.Object) []Suggestion {
	var out []Suggestion
	if listed == nil {
		return out
	}
	raw, _ := listed.Get("tools")
	rows, _ := raw.([]any)
	for _, r := range rows {
		o, ok := r.(*jsonx.Object)
		if !ok {
			continue
		}
		name := str(o, "name")
		if name == "" {
			continue
		}
		cls := str(o, "suggestedClass")
		if cls != "read" && cls != "write" {
			cls = "destructive"
		}
		out = append(out, Suggestion{Name: name, Class: cls})
	}
	return out
}

// WithSuggested is config.toolPrefs plus a pref for every listed tool the
// owner hasn't saved one for: on (as it already was), with its suggested
// class. Saved prefs keep their bytes. n is how many it added.
func WithSuggested(config *jsonx.Object, tools []Suggestion) (prefs *jsonx.Object, n int) {
	prefs = jsonx.NewMap[any]()
	if config != nil {
		if raw, ok := config.Get("toolPrefs"); ok {
			if o, ok := raw.(*jsonx.Object); ok {
				prefs = o.Clone()
			}
		}
	}
	for _, t := range tools {
		if _, saved := prefs.Get(t.Name); saved {
			continue
		}
		prefs.Set(t.Name, kit.O("enabled", true, "class", t.Class))
		n++
	}
	return prefs, n
}

func refused(text string) *jsonx.Object {
	return kit.O("isError", true, "content", []any{kit.O("type", "text", "text", text)})
}

// WithFileRefs swaps palui:attachment/<id> refs for data URLs just before the
// call. An external server never gets the owner's files; a refused ref comes
// back as an MCP error result.
func WithFileRefs(t connectors.Tool, d *db.DB, turn *connectors.TurnInfo, untrusted bool, key func() string) connectors.Tool {
	exec := t.Execute
	if exec == nil {
		return t
	}
	t.Execute = func(ctx context.Context, input json.RawMessage, call connectors.ToolCallInfo) (any, error) {
		v, err := jsonx.Parse(input)
		if err != nil || len(attachments.FindRefs(v)) == 0 {
			return exec(ctx, input, call)
		}
		if untrusted || turn == nil {
			return refused("Saved files can only be sent to a server the owner marked as their own."), nil
		}
		resolved, files, err := attachments.ResolveFileRefs(d, v, attachments.ResolveOptions{ChatID: turn.ChatID, MasterKey: key()})
		if err != nil {
			var re *attachments.RefError
			if errors.As(err, &re) {
				return refused(re.Msg), nil
			}
			return nil, err
		}
		b, err := jsonx.Marshal(resolved)
		if err != nil {
			return nil, err
		}
		out, err := exec(ctx, b, call)
		if err != nil {
			return nil, err
		}
		return attachments.ScrubFileURLs(normalize(out), files), nil
	}
	return t
}

func normalize(v any) any {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return v
	}
	p, err := jsonx.Parse(b)
	if err != nil {
		return v
	}
	return p
}

var unauthorizedRe = regexp.MustCompile(`(?i)401|unauthori[sz]ed`)

// ErrorText is errorText.
func ErrorText(err error) string {
	msg := err.Error()
	if unauthorizedRe.MatchString(msg) {
		return "The server rejected the token (401): " + msg
	}
	return msg
}

func masterKey() string { k, _ := config.MasterKey(config.Get()); return k }

// New is the runtime with injectable HTTP plumbing (tests).
func New(opts Options) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(c connectors.Context) (connectors.TestResult, error) {
			cfg := ConfigFrom(c.Config)
			ep, err := EndpointFor(cfg, c.Secret("token"), opts)
			if err != nil {
				return connectors.TestResult{OK: false, Detail: err.Error()}, nil
			}
			conn := NewConnection(ep, 0)
			defer conn.Close()
			tools, err := conn.ListTools(ctxOf(c), true, 0)
			if err != nil {
				return connectors.TestResult{OK: false, Detail: ErrorText(err)}, nil
			}
			n := len(tools)
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("%d tool%s available", n, kit.Plural(n)), Data: kit.O("tools", Listing(tools, cfg.ToolPrefs))}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			cfg := ConfigFrom(c.Config)
			ep, err := EndpointFor(cfg, c.Secret("token"), opts)
			if err != nil {
				return nil, err
			}
			conn := NewConnection(ep, 0)
			first, err := conn.ListTools(ctxOf(c), true, 0)
			if err != nil {
				conn.Close()
				return nil, errors.New(ErrorText(err))
			}
			untrusted := c.Trust == "external"
			log := c.Log
			if log == nil {
				log = logx.Silent
			}
			var mu sync.Mutex
			lastError := ""
			toolCount := len(first)
			return &connectors.RunningConnector{
				Stop: func(context.Context) error { return conn.Close() },
				Tools: func(ctx context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
					defs, err := conn.ListTools(ctx, false, 0)
					var picked []Selected
					var aiTools map[string]connectors.Tool
					if err == nil {
						picked = SelectTools(defs, cfg.ToolPrefs)
						ds := make([]ToolDef, len(picked))
						for i, p := range picked {
							ds[i] = p.Def
						}
						aiTools, err = conn.Tools(ctx, ds)
					}
					if err != nil {
						log.Warn("mcp tool listing failed, offering no tools this turn", map[string]any{"connector_id": c.ID, "error": err})
						conn.Reset()
						detail := ErrorText(err)
						mu.Lock()
						changed := detail != lastError
						lastError = detail
						mu.Unlock()
						if changed && c.SetStatus != nil {
							c.SetStatus("error", &detail)
						}
						return connectors.NewToolSet(), nil
					}
					mu.Lock()
					hadError := lastError != ""
					lastError = ""
					toolCount = len(defs)
					mu.Unlock()
					if hadError && c.SetStatus != nil {
						c.SetStatus("on", nil)
					}
					set := connectors.NewToolSet()
					t := turn
					for _, p := range picked {
						tool, ok := aiTools[p.Def.Name]
						if !ok {
							continue
						}
						title := p.Def.DisplayTitle()
						if title == "" {
							title = p.Def.Name
						}
						set.Set(p.Def.Name, connectors.PaluiTool{Tool: WithFileRefs(tool, c.DB, &t, untrusted, masterKey), Class: p.Class, Suggested: p.Suggested, Untrusted: untrusted, FlowApproval: p.FlowApproval, Title: title})
					}
					return set, nil
				},
				CallTool: func(ctx context.Context, name string, args *jsonx.Object) (connectors.ToolCallResult, error) {
					find := func(fresh bool) (*ToolDef, error) {
						defs, err := conn.ListTools(ctx, fresh, 0)
						if err != nil {
							return nil, err
						}
						for i := range defs {
							if defs[i].Name == name {
								return &defs[i], nil
							}
						}
						return nil, nil
					}
					def, err := find(false)
					if err == nil && def == nil {
						def, err = find(true)
					}
					if err != nil {
						return connectors.ToolCallResult{}, err
					}
					if def == nil {
						return connectors.ToolCallResult{}, fmt.Errorf("No tool %s on this server", name)
					}
					pref, ok := cfg.ToolPrefs[name]
					if ok && !pref.Enabled {
						return connectors.ToolCallResult{}, fmt.Errorf("Tool %s is turned off on the Connectors page", name)
					}
					cls := SuggestClass(def.Annotations)
					if ok && pref.Class != "" {
						cls = pref.Class
					}
					if cls != "read" {
						return connectors.ToolCallResult{Class: cls, Output: nil}, nil
					}
					out, err := conn.CallTool(ctx, name, args)
					if err != nil {
						return connectors.ToolCallResult{}, err
					}
					return connectors.ToolCallResult{Class: cls, Output: out}, nil
				},
				Live: func() *jsonx.Object {
					mu.Lock()
					defer mu.Unlock()
					var e any
					if lastError != "" {
						e = lastError
					}
					return kit.O("tools", toolCount, "url", cfg.URL, "error", e)
				},
				Action: func(ctx context.Context, name string, input *jsonx.Object) (*jsonx.Object, error) {
					if name != "listTools" {
						return nil, fmt.Errorf("Unknown action %s", name)
					}
					tools, err := conn.ListTools(ctx, true, 0)
					if err != nil {
						return nil, err
					}
					mu.Lock()
					toolCount = len(tools)
					mu.Unlock()
					return kit.O("tools", Listing(tools, cfg.ToolPrefs)), nil
				},
			}, nil
		},
	}
}

func ctxOf(c connectors.Context) context.Context {
	if c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}

// Runtime is the MCP connector runtime.
var Runtime = New(Options{})
