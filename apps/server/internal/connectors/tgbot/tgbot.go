// Package tgbot is an agent's own Telegram bot (connectors/src/tgbot, PLAN
// 19.6): the Telegram runtime under its own manifest; several instances
// allowed. Link it to an agent on the Agents page.
package tgbot

import (
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Manifest is { ...telegram, id, name, ... }: the Telegram manifest with the
// agent bot's own fields.
var Manifest = func() connectors.Manifest {
	m := telegram.Manifest
	m.ID = "tgbot"
	m.Name = "Agent bot (Telegram)"
	m.Icon = "telegram"
	m.Summary = "A Telegram bot of its own for one agent."
	m.Description = "Give an agent its own Telegram bot so people can add it to a group and @mention it by name. Create the bot with @BotFather, paste its token here, then pick it on the agent's page. Chats with this bot belong to that agent."
	m.Multiple = true
	m.Presets = []connectors.Preset{{ID: "agent", Label: "Agent bot", Config: jsonx.NewMap[any]()}}
	m.Setup = []connectors.SetupStep{
		{Field: "botToken", Label: "Bot token", Help: "Send this to @BotFather, then paste the token.", Command: "/newbot", Secret: true, Placeholder: "123456789:AA..."},
	}
	m.Docs = &connectors.Docs{Notes: []string{
		"Each agent bot needs its own token from @BotFather. The owner is the same Telegram account as on the main bot.",
		"Leave privacy mode on: in groups the bot then only sees commands, mentions and replies to it.",
	}}
	return m
}()

// New builds the runtime with injected Telegram options (tests).
func New(o telegram.Options) *connectors.Runtime { return telegram.New(o, Manifest) }

var Runtime = New(telegram.Options{})
