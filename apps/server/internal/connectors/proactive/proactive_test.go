package proactive_test

import (
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	proactiveconn "github.com/zakiego/palui/apps/server/internal/connectors/proactive"
	"github.com/zakiego/palui/apps/server/internal/proactive"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

func TestToolsAreOnlyOfferedInTheOwnersOwnTurns(t *testing.T) {
	d, telegramChat, _ := tu.Setup(t)
	web, err := chats.EnsureWebChat(d, tu.NOW)
	tu.Must(t, err)
	cases := []struct {
		turn connectors.TurnInfo
		want bool
	}{
		{connectors.TurnInfo{ChatID: web.ID, Trigger: "message", Mode: "owner"}, true},
		{connectors.TurnInfo{ChatID: web.ID, Trigger: "schedule", Mode: "owner"}, false},
		{connectors.TurnInfo{ChatID: web.ID, Trigger: "message", Mode: "members"}, false},
		{connectors.TurnInfo{ChatID: telegramChat, Trigger: "message"}, false},
	}
	for i, c := range cases {
		got, err := proactiveconn.Offer(d, c.turn)
		tu.Must(t, err)
		if got != c.want {
			t.Errorf("case %d: %v", i, got)
		}
	}
}

func TestUpdateTurnsOnAndPatchesFromChat(t *testing.T) {
	d := tu.DB(t)
	web, err := chats.EnsureWebChat(d, tu.NOW)
	tu.Must(t, err)
	tu.SeedConnector(t, d, "gcal-1", "tools", tu.SeedOptions{Type: "gcal"}, tu.NOW)
	turn := connectors.TurnInfo{ChatID: web.ID, Trigger: "message", Mode: "owner", Now: tu.NOW}
	set := proactiveconn.Tools(d, func() int64 { return tu.NOW }, turn)
	pt, _ := set.Get("update_proactive")
	if pt.Class != "write" {
		t.Fatal(pt.Class)
	}
	out := conntest.Exec(t, pt.Tool, `{"enabled":true,"chat":"this"}`)
	if out.B("ok") || !strings.Contains(out.S("error"), "source") {
		t.Fatalf("no source yet: %s", out)
	}
	out = conntest.Exec(t, pt.Tool, `{"enabled":true,"chat":"this","patch":{"calendar":{"connectorId":"gcal-1","departLeadMin":45,"onlineLeadMin":10,"notifyChanges":true},"quietHours":{"start":"23:00"}}}`)
	if !out.B("ok") || !out.B("enabled") || out.N("config.calendar.departLeadMin") != 45 || out.S("config.quietHours.start") != "23:00" || out.S("config.quietHours.end") != "07:00" {
		t.Fatalf("%s", out)
	}
	out = conntest.Exec(t, pt.Tool, `{"patch":{"checkEveryMin":1}}`)
	if out.B("ok") || !strings.Contains(out.S("error"), "between 15 and 120") {
		t.Fatalf("%s", out)
	}
	a, err := agents.GetDefault(d, tu.NOW)
	tu.Must(t, err)
	row, _, _, err := proactive.Get(d, a.ID)
	tu.Must(t, err)
	if !row.Enabled || *row.ChatID != web.ID || *row.NextTickAt != tu.NOW {
		t.Fatalf("%+v", row)
	}
	get, _ := set.Get("get_proactive")
	if got := conntest.Exec(t, get.Tool, `{}`); got.N("config.checkEveryMin") != 30 {
		t.Fatalf("%s", got)
	}
	check, _ := set.Get("check_proactive_now")
	if got := conntest.Exec(t, check.Tool, `{}`); !got.B("ok") {
		t.Fatalf("%s", got)
	}
}

func TestMuteFindsARecentNudgeByName(t *testing.T) {
	d := tu.DB(t)
	web, err := chats.EnsureWebChat(d, tu.NOW)
	tu.Must(t, err)
	a, err := agents.GetDefault(d, tu.NOW)
	tu.Must(t, err)
	_, err = d.Exec("INSERT INTO nudges (id, agent_id, key, kind, subject, status, snapshot, created_at, updated_at) VALUES ('ndg_1', ?, 'wa:3:1', 'wa_waiting', 'wa:chat:3', 'sent', '{\"name\":\"Grup Kantor\"}', ?, ?)", a.ID, tu.NOW, tu.NOW)
	tu.Must(t, err)
	set := proactiveconn.Tools(d, func() int64 { return tu.NOW }, connectors.TurnInfo{ChatID: web.ID, Trigger: "message", Now: tu.NOW})
	mute, _ := set.Get("mute_proactive")
	if out := conntest.Exec(t, mute.Tool, `{"who":"kantor"}`); !out.B("ok") || out.S("muted") != "Grup Kantor" {
		t.Fatalf("%s", out)
	}
	if out := conntest.Exec(t, mute.Tool, `{"who":"nobody"}`); out.B("ok") {
		t.Fatalf("%s", out)
	}
	unmute, _ := set.Get("unmute_proactive")
	if out := conntest.Exec(t, unmute.Tool, `{"who":"grup"}`); !out.B("ok") {
		t.Fatalf("%s", out)
	}
	list, _ := set.Get("list_nudges")
	if out := conntest.Exec(t, list.Tool, `{"status":"dropped"}`); out.N("count") != 1 {
		t.Fatalf("muting drops the open nudge: %s", out)
	}
}
