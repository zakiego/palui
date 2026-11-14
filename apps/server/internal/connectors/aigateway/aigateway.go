// Package aigateway is the ai-gateway model connector
// (connectors/src/ai-gateway), on the shared gateway runtime.
package aigateway

import (
	"regexp"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/cliproxy"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var SecretZod = []zod.Field{zod.F("apiKey", zod.String().Regex(regexp.MustCompile(`^zkg_`), `^zkg_`, "ai-gateway keys start with zkg_"))}

var Manifest = connectors.Manifest{
	ID:      "ai-gateway",
	Kind:    "model",
	Name:    "ai-gateway",
	Icon:    "network",
	Summary: "Fallback models through ai-gateway on the host.",
	Setup: []connectors.SetupStep{
		{Field: "baseUrl", Label: "Base URL", Placeholder: "http://host.docker.internal:8793", Optional: true},
		{Field: "apiKey", Label: "API key", Secret: true, Placeholder: "zkg_..."},
	},
	Docs:         &connectors.Docs{Commands: []string{"/connectors on ai-gateway", "/connectors off ai-gateway"}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    cliproxy.GatewayConfigZod("http://host.docker.internal:8793"),
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(cliproxy.GatewayConfigZod("http://host.docker.internal:8793")),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}

// Runtime is the ai-gateway connector runtime.
var Runtime = cliproxy.GatewayRuntime(Manifest, nil)
