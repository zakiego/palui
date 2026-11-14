// Package telegram is the Telegram channel connector (connectors/src/telegram,
// PLAN 4.1, 5.1): long polling against the Bot API, the owner claim through
// `/start <nonce>`, media in, HTML replies split at 4096 with inline buttons.
// The tgbot connector runs the same runtime under its own manifest.
package telegram

import (
	"regexp"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// ConfigZod is the manifest config. botUsername is learned at runtime from
// getMe, so the claim link works before polling starts.
var ConfigZod = zod.Object(
	zod.F("botUsername", zod.String().Optional()),
)

var SecretZod = []zod.Field{
	zod.F("botToken", zod.String().Regex(regexp.MustCompile(`^\d+:[\w-]{30,}$`), `^\d+:[\w-]{30,}$`, "That doesn't look like a bot token from @BotFather (123456789:AA...).")),
}

var Manifest = connectors.Manifest{
	ID:      "telegram",
	Kind:    "channel",
	Name:    "Telegram",
	Icon:    "telegram",
	Summary: "Chat from a Telegram bot.",
	Setup: []connectors.SetupStep{
		{Field: "botToken", Label: "Bot token", Help: "Send this to @BotFather, then paste the token.", Command: "/newbot", Secret: true, Placeholder: "123456789:AA..."},
		{Action: "claimOwner", Label: "Open the link and press Start"},
	},
	RemoveWarning: "This deletes the bot token. The bot stays in @BotFather.",
	Docs: &connectors.Docs{Notes: []string{
		"Dev and prod need different bot tokens: two getUpdates consumers on one token get HTTP 409.",
		"Photos, voice notes and documents up to 1 MB are passed to the agent.",
	}},
	SecretFields: kit.SecretNames(SecretZod),
	ConfigZod:    ConfigZod,
	SecretZod:    SecretZod,
	ParseConfig:  kit.ParseConfig(ConfigZod),
	ParseSecrets: kit.ParseSecrets(SecretZod),
}
