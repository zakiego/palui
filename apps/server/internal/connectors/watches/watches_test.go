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

func TestWatchForStartAt(t *testing.T) {
	t.Run("starts at the owner's wall-clock time, hours count from the start", func(t *testing.T) {
		e := setup(t)
		res := conntest.Call(t, e.tools, "watch_for", `{"topic":"Closing BBCA","question":"BBCA closing price on 11 Nov","doneWhen":"The final closing price for 11 Nov.","everyMin":15,"startAt":"2026-11-11T16:00","hours":5}`)
		if !res.B("ok") {
			t.Fatalf("%s", res)
		}
		// 16.00 WITA is 08.00 UTC, 30 hours after NOW (10.00 WITA the day before).
		w, _, err := watches.Get(e.db, res.S("id"))
		testutil.Must(t, err)
		if w.StartsAt == nil || *w.StartsAt != NOW+30*3600_000 || *w.NextCheckAt != NOW+30*3600_000 || w.ExpiresAt != NOW+35*3600_000 {
			t.Fatalf("%+v", w)
		}
		if want := `Oke, aku pantau "Closing BBCA" mulai Besok, Rabu 11 Nov, 16.00 WITA, tiap 15 menit sampai Besok, Rabu 11 Nov, 21.00 WITA. Sebelum itu nggak dicek sama sekali. Begitu ketemu aku kabarin di sini.`; res.S("message") != want {
			t.Fatal(res.S("message"))
		}
		list := conntest.Call(t, e.tools, "list_watches", `{}`)
		if !strings.Contains(list.String(), `"starts":"Besok, Rabu 11 Nov, 16.00 WITA"`) {
			t.Fatalf("%s", list)
		}
	})
	t.Run("reads startAt in the owner's zone, not UTC", func(t *testing.T) {
		e := setup(t)
		turn := e.turn
		turn.TZ = "Asia/Jakarta"
		tools := watchesconn.Tools(e.db, turn, func() int64 { return NOW })
		res := conntest.Call(t, tools, "watch_for", `{"topic":"t","question":"q","doneWhen":"d","startAt":"2026-11-11T16:00","until":"2026-11-11T21:00"}`)
		w, _, err := watches.Get(e.db, res.S("id"))
		testutil.Must(t, err)
		// 16.00 WIB is 09.00 UTC, an hour later than 16.00 WITA.
		if !res.B("ok") || *w.StartsAt != NOW+31*3600_000 || w.ExpiresAt != NOW+36*3600_000 {
			t.Fatalf("%s %+v", res, w)
		}
		if !strings.Contains(res.S("message"), "mulai Besok, Rabu 11 Nov, 16.00 WIB") {
			t.Fatal(res.S("message"))
		}
	})
	t.Run("without an end, it runs the default 7 days but never past 14 days from now", func(t *testing.T) {
		e := setup(t)
		res := conntest.Call(t, e.tools, "watch_for", `{"topic":"t","question":"q","doneWhen":"d","startAt":"2026-11-12T10:00"}`)
		w, _, _ := watches.Get(e.db, res.S("id"))
		if !res.B("ok") || w.ExpiresAt != NOW+(2+7)*24*3600_000 {
			t.Fatalf("%s %+v", res, w)
		}
		res = conntest.Call(t, e.tools, "watch_for", `{"topic":"t","question":"q","doneWhen":"d","startAt":"2026-11-20T10:00"}`)
		w, _, _ = watches.Get(e.db, res.S("id"))
		if !res.B("ok") || w.ExpiresAt != NOW+14*24*3600_000 {
			t.Fatalf("%s %+v", res, w)
		}
	})
	t.Run("refuses a bad start", func(t *testing.T) {
		e := setup(t)
		for in, msg := range map[string]string{
			`"startAt":"2026-11-10T09:00"`:                            "already passed",
			`"startAt":"2026-11-11T21:00","until":"2026-11-11T16:00"`: "before the end",
			`"startAt":"2026-11-11T16:00","until":"2026-11-11T16:00"`: "before the end",
			`"startAt":"2026-11-25T10:00"`:                            "before the end",
			`"startAt":"2026-11-24T10:00","hours":48`:                 "at most 14 days",
			`"startAt":"2026-02-30T10:00"`:                            "",
		} {
			res := conntest.Call(t, e.tools, "watch_for", `{"topic":"t","question":"q","doneWhen":"d",`+in+`}`)
			if res.B("ok") || !strings.Contains(res.S("error"), msg) {
				t.Errorf("%s: %s", in, res)
			}
		}
		if n, _ := watches.CountActive(e.db, e.chatID); n != 0 {
			t.Fatal(n)
		}
	})
}
