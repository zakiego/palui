// Package httpreq is the HTTP API connector (type id "http"): one instance
// per API key, limited to the domains the owner names. The model calls the
// API through two tools and never sees the key.
package httpreq

import (
	"regexp"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var (
	headerNameRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")
	paramRe      = regexp.MustCompile(`^[A-Za-z0-9_.~-]+$`)
)

// hasControlChars: a C0 control character or DEL.
func hasControlChars(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

func noControlChars(s string) bool { return !hasControlChars(s) }

func hasValueSlot(s string) bool { return strings.Contains(s, "{value}") && !hasControlChars(s) }

// ConfigZod is the instance's config.
var ConfigZod = zod.Object(
	zod.F("domains", zod.String().Trim().Min(1).Max(1000).Refine(validDomains, "Use host names like api.example.com or *.example.com, separated by commas")),
	zod.F("auth", zod.Default(zod.Enum("header", "query", "none"), "header")),
	zod.F("headerName", zod.Default(zod.String().Trim().Min(1).Max(100).Regex(headerNameRe, "^[A-Za-z0-9!#$%&'*+.^_`|~-]+$", "Header names use letters, digits and dashes"), "Authorization")),
	zod.F("valueTemplate", zod.Default(zod.String().Trim().Min(1).Max(200).Refine(hasValueSlot, "Put {value} where the key goes"), "Bearer {value}")),
	zod.F("queryParam", zod.String().Trim().Min(1).Max(100).Regex(paramRe, "^[A-Za-z0-9_.~-]+$", "Parameter names use letters, digits, dots, dashes and underscores").Optional()),
	zod.F("policy", zod.Default(zod.Enum("ask", "allow"), "ask")),
)

// SecretZod is the key.
var SecretZod = []zod.Field{zod.F("value", zod.String().Min(8).Max(4000).Refine(noControlChars, "The key can't contain line breaks").Optional())}

var keyModes = []string{"header", "query"}

var Manifest = connectors.Manifest{
	ID:       "http",
	Kind:     "tools",
	Name:     "HTTP API",
	Icon:     "key-round",
	Summary:  "Call an HTTP API with a key Palui keeps and never shows the model.",
	Multiple: true,
	Trust:    "external",
	Setup: []connectors.SetupStep{
		{Field: "domains", Label: "Allowed domains", Placeholder: "api.example.com", Help: "Hosts this connector may call, separated by commas. *.example.com covers subdomains. The key is never sent anywhere else."},
		{Field: "auth", Label: "How the key is sent", AskFirst: true, Help: "header: a request header. query: a URL parameter. none: no key."},
		{Field: "headerName", Label: "Header name", AskFirst: true, Placeholder: "Authorization", ShowIf: &connectors.ShowIf{Field: "auth", In: []string{"header"}}},
		{Field: "valueTemplate", Label: "Header value", AskFirst: true, Placeholder: "Bearer {value}", Help: "{value} is replaced with the key.", ShowIf: &connectors.ShowIf{Field: "auth", In: []string{"header"}}},
		{Field: "queryParam", Label: "Query parameter", AskFirst: true, Placeholder: "api_key", ShowIf: &connectors.ShowIf{Field: "auth", In: []string{"query"}}},
		{Field: "value", Label: "Key", Secret: true, Optional: true, AskFirst: true, ShowIf: &connectors.ShowIf{Field: "auth", In: keyModes}},
		{Field: "policy", Label: "Approval", AskFirst: true, Help: "ask: every call waits for your yes. allow: reads run on their own, writes follow the agent's tool permissions."},
	},
	RemoveWarning: "This removes the key from Palui. The service isn't touched.",
	Docs: &connectors.Docs{Notes: []string{
		"Tools are namespaced <connector id>__<tool>, for example http-elevenlabs__http_request.",
		"The key is sent only to the allowed domains, over https, and is removed from everything the model sees.",
		"Shared chats never get these tools.",
	}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}
