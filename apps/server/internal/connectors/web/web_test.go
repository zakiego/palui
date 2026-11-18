package web_test

import (
	"context"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/web"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestPublishesRepliesOnTheTurnBusAndAcksWithAMessageID(t *testing.T) {
	d := testutil.DB(t)
	c, err := chats.EnsureWebChat(d, testutil.NOW)
	testutil.Must(t, err)
	bus := chat.NewBus()
	var seen []chat.TurnEvent
	bus.Subscribe(func(e chat.TurnEvent) { seen = append(seen, e) })
	h, err := web.New(bus).Start(conntest.Ctx(t, d, "web", nil, nil))
	testutil.Must(t, err)
	if !h.Channel.Reachable() || h.Channel.Channel() != "web" {
		t.Fatal("not reachable")
	}
	r, err := h.Channel.Send(context.Background(), "web", db.OutboxPayload{Text: "halo"})
	testutil.Must(t, err)
	want := chat.TurnEvent{Type: "message", ChatID: c.ID, MessageID: r.ChannelMsgID, Role: "assistant", Text: "halo"}
	if len(seen) != 1 || jsonx.Stringify(seen[0]) != jsonx.Stringify(want) {
		t.Fatalf("seen %s", jsonx.Stringify(seen))
	}
	if r.ChannelMsgID[:7] != "webmsg_" {
		t.Fatal(r.ChannelMsgID)
	}
}

func TestUnknownChatFallsBackToTheExternalID(t *testing.T) {
	bus := chat.NewBus()
	var seen []chat.TurnEvent
	bus.Subscribe(func(e chat.TurnEvent) { seen = append(seen, e) })
	h, err := web.New(bus).Start(conntest.Ctx(t, nil, "web", nil, nil))
	testutil.Must(t, err)
	_, err = h.Channel.Send(context.Background(), "nope", db.OutboxPayload{Text: "x"})
	testutil.Must(t, err)
	if seen[0].ChatID != "nope" {
		t.Fatal(seen[0].ChatID)
	}
	tr, _ := web.Runtime.Test(conntest.Ctx(t, nil, "web", nil, nil))
	if !tr.OK || tr.Detail != "Web chat is built in." {
		t.Fatal(tr)
	}
}

func TestMediaShowsAsALineNamingTheFile(t *testing.T) {
	bus := chat.NewBus()
	var seen []chat.TurnEvent
	bus.Subscribe(func(e chat.TurnEvent) { seen = append(seen, e) })
	h, err := web.New(bus).Start(conntest.Ctx(t, nil, "web", nil, nil))
	testutil.Must(t, err)
	_, err = h.Channel.Send(context.Background(), "web", db.OutboxPayload{Text: "Ini contohnya", Media: &db.OutboxMedia{ID: "med_1", Kind: db.MediaImage, Filename: "inhaler.jpg", Bytes: []byte{1}}})
	testutil.Must(t, err)
	if seen[0].Text != "[Image: inhaler.jpg]\nIni contohnya" {
		t.Fatalf("%q", seen[0].Text)
	}
	if got := web.MediaText(db.OutboxPayload{Media: &db.OutboxMedia{Kind: db.MediaDocument}}); got != "[File]" {
		t.Fatalf("%q", got)
	}
}
