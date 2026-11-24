package wainbox_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/wainbox"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp"
	"github.com/zakiego/palui/apps/server/internal/connectors/whatsapp/watest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

// 2026-11-10 10:00 WITA.
var now = tu.NOW

type env struct {
	mu      sync.Mutex
	sockets []*watest.FakeSocket
	clock   int64
	ctx     *conntest.ChannelCtx
	rt      *connectors.Runtime
}

func (e *env) sock() *watest.FakeSocket {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sockets[len(e.sockets)-1]
}

func setup(t *testing.T) (*env, *connectors.RunningConnector) {
	t.Helper()
	e := &env{clock: now}
	e.rt = wainbox.New(wainbox.Options{
		Clock: func() int64 { return e.clock },
		WhatsApp: whatsapp.Options{CreateSocket: func(context.Context, string) (whatsapp.Socket, error) {
			s := watest.New(true)
			s.GroupSubjects["120363000000000001@g.us"] = "Grup Kantor"
			e.mu.Lock()
			e.sockets = append(e.sockets, s)
			e.mu.Unlock()
			return s, nil
		}},
	})
	e.ctx = conntest.NewChannelCtx(t, "wa-inbox-1", "wa-inbox", jsonx.NewMap[any](), nil, t.TempDir())
	r, err := e.rt.Start(e.ctx.Context)
	tu.Must(t, err)
	t.Cleanup(func() { _ = r.Stop(context.Background()) })
	e.sock().OpenConn("628000001472:3@s.whatsapp.net")
	return e, r
}

func msg(chat, text string, fromMe bool, atSec int64, push string) *whatsapp.Message {
	return &whatsapp.Message{
		Key:       whatsapp.Key{RemoteJID: chat, FromMe: fromMe, ID: "m" + text},
		Message:   &waE2E.Message{Conversation: proto.String(text)},
		Timestamp: atSec,
		PushName:  push,
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func rows(t *testing.T, d *db.DB) map[string]db.WaInboxChat {
	t.Helper()
	r, err := d.Query("SELECT " + db.WaInboxChatCols + " FROM wa_inbox_chats")
	list, err := db.All(r, err, db.ScanWaInboxChat)
	tu.Must(t, err)
	out := map[string]db.WaInboxChat{}
	for _, c := range list {
		out[c.ChatJID] = c
	}
	return out
}

func TestTheInboxRecordsWaitsAndNeverAnswers(t *testing.T) {
	e, r := setup(t)
	if r.Channel != nil {
		t.Fatal("the inbox must not be a channel the outbox can send through")
	}
	d := e.ctx.DB
	s := e.sock()
	base := now / 1000
	budi, istri := "6281111111111@s.whatsapp.net", "6282222222222@s.whatsapp.net"
	s.Message(msg(budi, "Pak, unit Banjarbaru masih ada?", false, base-3*3600, "Pak Budi"), "notify")
	s.Message(msg(budi, "Bisa survei besok?", false, base-2*3600, "Pak Budi"), "notify")
	s.Message(msg(istri, "Pulang jam berapa?", false, base-3600, "Istri"), "notify")
	s.Message(msg(istri, "Jam 6 ya", true, base-1800, "Zaki"), "notify")
	s.Message(msg("120363000000000001@g.us", "Rapat jam 2", false, base-600, "Andi"), "notify")
	s.Message(msg("status@broadcast", "story", false, base, "Siapa"), "notify")
	// A note to self and a reaction aren't waits.
	s.Message(msg("628000001472@s.whatsapp.net", "catatan", false, base, "Zaki"), "notify")
	s.Message(&whatsapp.Message{Key: whatsapp.Key{RemoteJID: istri, ID: "r1"}, Message: &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}}, Timestamp: base}, "notify")
	waitFor(t, func() bool { return len(rows(t, d)) == 3 })
	got := rows(t, d)
	b := got[budi]
	if b.WaitingSince == nil || *b.WaitingSince != (base-3*3600)*1000 || b.Unanswered != 2 || b.Name != "Pak Budi" || b.Preview != "Bisa survei besok?" || b.Kind != "dm" {
		t.Fatalf("%+v", b)
	}
	if i := got[istri]; i.WaitingSince != nil || i.Unanswered != 0 || i.Name != "Istri" {
		t.Fatalf("the owner's reply ends the wait: %+v", i)
	}
	if g := got["120363000000000001@g.us"]; g.Kind != "group" || g.Name != "Grup Kantor" {
		t.Fatalf("%+v", g)
	}
	if len(s.SentList()) != 0 || len(s.PresenceList()) != 0 || e.ctx.InboundCount() != 0 {
		t.Fatal("the inbox sent, typed or routed something")
	}

	set, err := r.Tools(context.Background(), connectors.TurnInfo{TZ: "Asia/Makassar", Now: now})
	tu.Must(t, err)
	waiting, _ := set.Get("waiting_chats")
	if waiting.Class != "read" || !waiting.Untrusted {
		t.Fatalf("%+v", waiting)
	}
	out := conntest.Exec(t, waiting.Tool, `{}`)
	if out.N("count") != 1 || out.S("chats.0.id") != "6281111111111" || out.N("chats.0.waitingMinutes") != 180 || out.S("chats.0.waitingSince") != "2026-11-10T07:00:00+08:00" || out.N("chats.0.unanswered") != 2 {
		t.Fatalf("%s", out)
	}
	if out := conntest.Exec(t, waiting.Tool, `{"include_groups":true}`); out.N("count") != 2 {
		t.Fatalf("%s", out)
	}
	if out := conntest.Exec(t, waiting.Tool, `{"min_minutes":240}`); out.N("count") != 0 {
		t.Fatalf("%s", out)
	}
	recent, _ := set.Get("list_chats")
	if out := conntest.Exec(t, recent.Tool, `{}`); out.N("count") != 3 || out.S("chats.0.name") != "Grup Kantor" || out.S("chats.1.lastOut") != "2026-11-10T09:30:00+08:00" {
		t.Fatalf("%s", out)
	}

	// The owner answers Pak Budi from the phone: the wait closes.
	s.Message(msg(budi, "Bisa, jam 10", true, base, "Zaki"), "notify")
	waitFor(t, func() bool { return rows(t, d)[budi].WaitingSince == nil })
	// A message that arrives late but is older than the reply doesn't reopen it.
	s.Message(msg(budi, "(telat)", false, base-60, "Pak Budi"), "notify")
	time.Sleep(20 * time.Millisecond)
	if b := rows(t, d)[budi]; b.WaitingSince != nil {
		t.Fatalf("%+v", b)
	}

	// Removing the connector forgets its chats.
	tu.Must(t, wainbox.RemoveChats(d)(db.Connector{ID: "wa-inbox-1", Type: "wa-inbox"}, true))
	if n := len(rows(t, d)); n != 0 {
		t.Fatal(n)
	}
}

func TestTestNeedsNoOwnerNumber(t *testing.T) {
	ctx := conntest.NewChannelCtx(t, "wa-inbox-1", "wa-inbox", jsonx.NewMap[any](), nil, t.TempDir())
	res, err := wainbox.Runtime.Test(ctx.Context)
	tu.Must(t, err)
	if !res.OK || res.Detail != "Not paired yet. Start pairing to link the number." {
		t.Fatalf("%+v", res)
	}
	if wainbox.Runtime.Manifest.ID != "wa-inbox" || wainbox.Runtime.Manifest.Kind != "tools" {
		t.Fatal("manifest")
	}
}
