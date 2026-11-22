// Package gateway is the generic model gateway connector: any OpenAI- or
// Anthropic-compatible endpoint (LiteLLM, OpenRouter, a self-hosted proxy),
// on the gateway runtime cliproxy shares. Several can be added; each instance
// id ("gateway-<slug>") is what model roles point at.
package gateway

import (
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/cliproxy"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var ConfigZod = cliproxy.RequiredGatewayConfigZod()

var SecretZod = []zod.Field{zod.F("apiKey", zod.String().Min(1))}

var Manifest = connectors.Manifest{
	ID:       "gateway",
	Kind:     "model",
	Name:     "Model gateway",
	Icon:     "network",
	Summary:  "Models from any OpenAI- or Anthropic-compatible endpoint.",
	Multiple: true,
	Setup: []connectors.SetupStep{
		{Field: "baseUrl", Label: "Base URL", Placeholder: "https://llm.example.com", Help: "The address before /v1, like a LiteLLM or OpenRouter endpoint or your own proxy."},
		{Field: "apiKey", Label: "API key", Secret: true, Help: "Sent as a bearer token and as x-api-key."},
	},
	Docs:         &connectors.Docs{Notes: []string{"Each gateway you add gets its own id, like gateway-openrouter. Pick its models per role in Settings."}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

// Runtime is the model gateway connector runtime.
var Runtime = cliproxy.GatewayRuntime(Manifest, nil)
