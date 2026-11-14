// Package web is the web chat channel (connectors/src/web): replies go to the
// turn bus, which the server streams to the dashboard over SSE. Nothing to
// connect, so it's always up.
package web

import (
	"context"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var Manifest = connectors.Manifest{
	ID:          "web",
	Kind:        "channel",
	Name:        "Web chat",
	Icon:        "message-square",
	Summary:     "Chat from the dashboard. Always on.",
	Setup:       []connectors.SetupStep{},
	Builtin:     true,
	ParseConfig: kit.ParseConfig(zod.Object()),
}

// New is webRuntime(bus): replies are published on bus.
func New(bus *chat.Bus) *connectors.Runtime {
	return &connectors.Runtime{
		Manifest: Manifest,
		Test: func(connectors.Context) (connectors.TestResult, error) {
			return connectors.TestResult{OK: true, Detail: "Web chat is built in."}, nil
		},
		Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
			return &connectors.RunningConnector{
				Stop:    func(context.Context) error { return nil },
				Channel: &channel{db: c.DB, bus: bus},
			}, nil
		},
	}
}

// Runtime publishes on chat.DefaultBus, the bus the SSE stream reads.
var Runtime = New(chat.DefaultBus)

type channel struct {
	db  *db.DB
	bus *chat.Bus
}

func (*channel) Channel() string { return "web" }
func (*channel) Reachable() bool { return true }
func (w *channel) Send(_ context.Context, chatExternalID string, payload db.OutboxPayload) (connectors.SendResult, error) {
	chatID := chatExternalID
	if c, ok, err := chats.Find(w.db, "web", chatExternalID, ""); err != nil {
		return connectors.SendResult{}, err
	} else if ok {
		chatID = c.ID
	}
	messageID := secret.NewID("webmsg")
	w.bus.Publish(chat.TurnEvent{Type: "message", ChatID: chatID, MessageID: messageID, Role: "assistant", Text: payload.Text})
	return connectors.SendResult{ChannelMsgID: messageID}, nil
}
