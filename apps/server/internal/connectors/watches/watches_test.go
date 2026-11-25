package watches_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	watchesconn "github.com/zakiego/palui/apps/server/internal/connectors/watches"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/watches"
)

// Tuesday 2026-11-10 10:00 WITA.
const NOW = int64(1794621600000) - 4*24*3600_000

const WITA = "Asia/Makassar"

type env struct {
	db      *db.DB
	chatID  string
	otherID string
	turn    connectors.TurnInfo
	tools   *connectors.ToolSet
}

// A group chat where Bayu (not the owner) asked the agent in run_1.
func setup(t *testing.T) env {
	d := testutil.DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120363@g.us", Kind: "group"}, NOW)
	testutil.Must(t, err)
	other, err := chats.Ensure(d, chats.EnsureInput{Channel: "web", ExternalID: "web"}, NOW)
	testutil.Must(t, err)
	_, err = d.Exec("INSERT INTO messages (id, chat_id, role, content, text, sender_name, from_owner, status, run_id, created_at) VALUES ('msg_1', ?, 'user', '{}', 'kabarin ya', 'Bayu Nurkholis', 0, 'handled', 'run_1', ?)", chat.ID, NOW)
	testutil.Must(t, err)
	turn := connectors.TurnInfo{RunID: "run_1", ChatID: chat.ID, Channel: "whatsapp", Trigger: "message", TZ: WITA, Now: NOW, Mode: "outside"}
	return env{d, chat.ID, other.ID, turn, watchesconn.Tools(d, turn, func() int64 { return NOW })}
}

func TestManifestIsABuiltinSkill(t *testing.T) {
	m := watchesconn.Manifest
	if m.ID != "watches" || m.Kind != "skill" || !m.Builtin || m.Icon != "eye" {
		t.Fatalf("%+v", m)
	}
}

func TestToolsAndClasses(t *testing.T) {
	e := setup(t)
	if got := strings.Join(conntest.Classes(e.tools), ","); got != "watch_for:write,list_watches:read,cancel_watch:write" {
		t.Fatal(got)
	}
}

func TestOfferedInMessageTurnsOnly(t *testing.T) {
	for trigger, want := range map[string]bool{"message": true, "approval": true, "schedule": false, "flow": false, "hook": false, "reminder": false, "watch": false} {
		if got := watchesconn.Offer(connectors.TurnInfo{Trigger: trigger}); got != want {
			t.Errorf("%s: %v", trigger, got)
		}
	}
	r, err := watchesconn.Runtime.Start(connectors.Context{DB: testutil.DB(t)})
	testutil.Must(t, err)
	set, err := r.Tools(context.Background(), connectors.TurnInfo{Trigger: "watch"})
	testutil.Must(t, err)
	if set.Len() != 0 {
		t.Fatal("a watch check got watch tools")
	}
}

func TestWatchForCreatesInTheTurnsChatForAGuest(t *testing.T) {
	e := setup(t)
	res := conntest.Call(t, e.tools, "watch_for", `{"topic":"Laba resmi Danantara","question":"Danantara audited 2025 net profit","doneWhen":"Audited figure; the Rp326 T claim doesn't count."}`)
	if !res.B("ok") {
		t.Fatalf("%s", res)
	}
	if want := `Oke, aku pantau "Laba resmi Danantara" tiap 30 menit sampai Selasa 17 Nov, 10.00 WITA. Begitu ketemu aku kabarin di sini.`; res.S("message") != want {
		t.Fatal(res.S("message"))
	}
	w, ok, err := watches.Get(e.db, res.S("id"))
	testutil.Must(t, err)
	if !ok || w.ChatID != e.chatID || w.CreatedBy != "Bayu Nurkholis" || w.ExpiresAt != NOW+7*24*3600_000 {
		t.Fatalf("%+v", w)
	}
	res = conntest.Call(t, e.tools, "watch_for", `{"topic":"t","question":"q","doneWhen":"d","everyMin":120,"until":"2026-11-10T18:00"}`)
	if !strings.Contains(res.S("message"), "tiap 2 jam sampai Hari ini, 18.00 WITA") {
		t.Fatal(res.S("message"))
	}
}

func TestListAndCancelStayInTheChat(t *testing.T) {
	e := setup(t)
	w, err := watches.Create(e.db, watches.CreateInput{ChatID: e.otherID, Topic: "other", Question: "q", DoneWhen: "d", ExpiresAt: NOW + 3600_000}, NOW)
	testutil.Must(t, err)
	res := conntest.Call(t, e.tools, "cancel_watch", `{"id":"`+w.ID+`"}`)
	if res.B("ok") || !strings.Contains(res.S("error"), "No watch") {
		t.Fatalf("%s", res)
	}
	created := conntest.Call(t, e.tools, "watch_for", `{"topic":"Laba","question":"q","doneWhen":"d"}`)
	list := conntest.Call(t, e.tools, "list_watches", `{}`)
	if list.N("count") != 1 {
		t.Fatalf("%s", list)
	}
	res = conntest.Call(t, e.tools, "cancel_watch", `{"id":"`+created.S("id")+`"}`)
	if !res.B("ok") || res.S("message") != `Oke, pantauan "Laba" dihentikan.` {
		t.Fatalf("%s", res)
	}
	if conntest.Call(t, e.tools, "list_watches", `{"ended":true}`).N("count") != 1 {
		t.Fatal("not in the ended list")
	}
}
