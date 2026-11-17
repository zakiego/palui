package reminders_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	jobsconn "github.com/zakiego/palui/apps/server/internal/connectors/jobs"
	remindersconn "github.com/zakiego/palui/apps/server/internal/connectors/reminders"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/testutil"
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

func setup(t *testing.T) env {
	d := testutil.DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, NOW)
	testutil.Must(t, err)
	other, err := chats.Ensure(d, chats.EnsureInput{Channel: "web", ExternalID: "web"}, NOW)
	testutil.Must(t, err)
	turn := connectors.TurnInfo{RunID: "run_1", ChatID: chat.ID, Channel: "telegram", Trigger: "message", TZ: WITA, Now: NOW}
	return env{d, chat.ID, other.ID, turn, remindersconn.Tools(d, turn, func() int64 { return NOW })}
}

func TestManifestIsABuiltinSkillWithThePauseNote(t *testing.T) {
	m := remindersconn.Manifest
	if m.ID != "reminders" || m.Kind != "skill" || !m.Builtin || m.Icon != "alarm-clock" {
		t.Fatalf("%+v", m)
	}
	if m.PauseNote != "Pausing hides the reminder tools. Reminders already set still fire." {
		t.Fatal(m.PauseNote)
	}
	j := jobsconn.Manifest
	if j.ID != "jobs" || j.Kind != "skill" || !j.Builtin || j.Icon != "calendar-clock" {
		t.Fatalf("%+v", j)
	}
}

func TestExposesTheFiveToolsWithClasses(t *testing.T) {
	e := setup(t)
	if got := strings.Join(conntest.Classes(e.tools), ","); got != "set_reminder:write,list_reminders:read,update_reminder:write,cancel_reminder:write,snooze_reminder:write" {
		t.Fatal(got)
	}
}

func TestSetReminderCreatesInTheTurnsChatAndEchoesTheTime(t *testing.T) {
	e := setup(t)
	res := conntest.Call(t, e.tools, "set_reminder", `{"text":"bayar listrik","localDateTime":"2026-11-11T09:00"}`)
	if !res.B("ok") || res.S("echo") != "Besok, Rabu 11 Nov, 09.00 WITA" {
		t.Fatalf("%s", res)
	}
	if res.S("message") != `Oke, aku ingetin "bayar listrik": Besok, Rabu 11 Nov, 09.00 WITA.` {
		t.Fatal(res.S("message"))
	}
	r, _, err := reminders.Get(e.db, res.S("id"))
	testutil.Must(t, err)
	if r.ChatID != e.chatID {
		t.Fatal("wrong chat")
	}
	weekly := conntest.Call(t, e.tools, "set_reminder", `{"text":"laporan","localDateTime":"2026-11-16T08:00","rrule":"FREQ=WEEKLY;BYDAY=MO"}`)
	if weekly.S("message") != `Oke, aku ingetin "laporan": Tiap Senin, 08.00 WITA. Pertama: Senin 16 Nov, 08.00 WITA.` {
		t.Fatal(weekly.S("message"))
	}
}

func TestSetReminderReturnsErrorsInsteadOfThrowing(t *testing.T) {
	e := setup(t)
	for _, in := range []string{
		`{"text":"x","localDateTime":"2026-11-09T09:00"}`,
		`{"text":"x","localDateTime":"2026-11-11T09:00","tz":"Mars/Base"}`,
		`{"text":"x","localDateTime":"2026-11-11T09:00","rrule":"FREQ=OFTEN"}`,
	} {
		res := conntest.Call(t, e.tools, "set_reminder", in)
		if res.Err != nil || res.B("ok") {
			t.Fatalf("%s: %s %v", in, res, res.Err)
		}
	}
}

func TestUsesAnExplicitZone(t *testing.T) {
	e := setup(t)
	res := conntest.Call(t, e.tools, "set_reminder", `{"text":"call","localDateTime":"2026-11-11T09:00","tz":"Asia/Jakarta"}`)
	if res.S("echo") != "Besok, Rabu 11 Nov, 09.00 WIB" {
		t.Fatal(res.S("echo"))
	}
	r, _, _ := reminders.Get(e.db, res.S("id"))
	if got := reminders.UTCToLocal(r.FireAt, "Asia/Jakarta"); got != "2026-11-11T09:00:00" {
		t.Fatal(got)
	}
}

func TestListsChangesSnoozesAndCancelsOnlyThisChatsReminders(t *testing.T) {
	e := setup(t)
	a := conntest.Call(t, e.tools, "set_reminder", `{"text":"a","localDateTime":"2026-11-11T09:00"}`)
	otherTurn := e.turn
	otherTurn.ChatID = e.otherID
	other := remindersconn.Tools(e.db, otherTurn, func() int64 { return NOW })
	b := conntest.Call(t, other, "set_reminder", `{"text":"b","localDateTime":"2026-11-11T10:00"}`)

	list := conntest.Call(t, e.tools, "list_reminders", `{"filter":"upcoming","limit":20}`)
	if ids := list.Strs("reminders", "id"); len(ids) != 1 || ids[0] != a.S("id") {
		t.Fatalf("%v", ids)
	}
	changed := conntest.Call(t, e.tools, "update_reminder", `{"id":"`+a.S("id")+`","localDateTime":"2026-11-12T07:30"}`)
	if changed.S("message") != `Sip, pengingat "a" sekarang Kamis 12 Nov, 07.30 WITA.` {
		t.Fatal(changed.S("message"))
	}
	if got := conntest.Call(t, e.tools, "snooze_reminder", `{"id":"`+a.S("id")+`","preset":"1h"}`).S("message"); got != "Oke, ditunda sampai 11.00 WITA." {
		t.Fatal(got)
	}
	if got := conntest.Call(t, e.tools, "snooze_reminder", `{"id":"`+a.S("id")+`","localDateTime":"2026-11-10T15:00"}`).S("message"); got != "Oke, ditunda sampai 15.00 WITA." {
		t.Fatal(got)
	}
	if conntest.Call(t, e.tools, "snooze_reminder", `{"id":"`+a.S("id")+`"}`).B("ok") {
		t.Fatal("snooze without preset")
	}
	if conntest.Call(t, e.tools, "cancel_reminder", `{"id":"`+b.S("id")+`"}`).B("ok") {
		t.Fatal("cancelled another chat's reminder")
	}
	if conntest.Call(t, e.tools, "update_reminder", `{"id":"`+b.S("id")+`","text":"hijack"}`).B("ok") {
		t.Fatal("changed another chat's reminder")
	}
	rb, _, _ := reminders.Get(e.db, b.S("id"))
	if rb.Text != "b" {
		t.Fatal(rb.Text)
	}
	if got := conntest.Call(t, e.tools, "cancel_reminder", `{"id":"`+a.S("id")+`"}`).S("message"); got != `Oke, pengingat "a" dibatalin.` {
		t.Fatal(got)
	}
	if got := conntest.Call(t, e.tools, "list_reminders", `{"filter":"done","limit":20}`).S("reminders.0.status"); got != "cancelled" {
		t.Fatal(got)
	}
	if got := conntest.Call(t, e.tools, "list_reminders", `{"filter":"upcoming","limit":20}`).S("message"); got != "Nggak ada pengingat aktif." {
		t.Fatal(got)
	}
}

func TestTestAndStartWork(t *testing.T) {
	e := setup(t)
	c := conntest.Ctx(t, e.db, "reminders", nil, nil)
	res, err := remindersconn.Runtime.Test(c)
	testutil.Must(t, err)
	if !res.OK || res.Detail != "Ready, no active reminders" {
		t.Fatalf("%+v", res)
	}
	h, err := remindersconn.Runtime.Start(c)
	testutil.Must(t, err)
	set, err := h.Tools(context.Background(), e.turn)
	testutil.Must(t, err)
	if _, ok := set.Get("set_reminder"); !ok {
		t.Fatal("no set_reminder")
	}
	testutil.Must(t, h.Stop(context.Background()))
}

func TestJobToolsListJobsWithTheScheduleAndNextRun(t *testing.T) {
	e := setup(t)
	_, err := jobs.Create(e.db, jobs.DailyBriefing(e.chatID, WITA, NOW), NOW)
	testutil.Must(t, err)
	res := conntest.Call(t, jobsconn.Tools(e.db, func() int64 { return NOW }, ""), "list_jobs", `{"includePaused":true}`)
	if res.S("jobs.0.name") != "Briefing pagi" || res.S("jobs.0.schedule") != "Tiap hari, 07.00 WITA" || res.S("jobs.0.next") != "Besok, Rabu 11 Nov, 07.00 WITA" || !res.B("jobs.0.enabled") {
		t.Fatalf("%s", res)
	}
	pt, _ := jobsconn.Tools(e.db, nil, "").Get("list_jobs")
	if pt.Class != "read" {
		t.Fatal(pt.Class)
	}
	tr, err := jobsconn.Runtime.Test(conntest.Ctx(t, e.db, "jobs", nil, nil))
	testutil.Must(t, err)
	if !tr.OK || tr.Detail != "1 active job" {
		t.Fatalf("%+v", tr)
	}
}

func TestJobToolsListOnlyTheJobsInTheAgentsChats(t *testing.T) {
	e := setup(t)
	palui, err := agents.GetDefault(e.db, NOW)
	testutil.Must(t, err)
	fin, err := agents.Create(e.db, agents.Input{Slug: "finansial", Name: testutil.Ptr("Finansial"), Persona: testutil.Ptr("Catat keuangan.")}, NOW)
	testutil.Must(t, err)
	group, err := chats.Ensure(e.db, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120363@g.us", Kind: "group", AgentID: &fin.ID}, NOW)
	testutil.Must(t, err)
	mine, err := jobs.Create(e.db, jobs.DailyBriefing(e.chatID, WITA, NOW), NOW)
	testutil.Must(t, err)
	theirs, err := jobs.Create(e.db, jobs.DailyBriefing(group.ID, WITA, NOW), NOW)
	testutil.Must(t, err)
	for _, c := range []struct{ agent, want string }{{palui.ID, mine.ID}, {"", mine.ID}, {fin.ID, theirs.ID}} {
		res := conntest.Call(t, jobsconn.Tools(e.db, func() int64 { return NOW }, c.agent), "list_jobs", `{"includePaused":true}`)
		if got := strings.Join(res.Strs("jobs", "id"), ","); got != c.want {
			t.Fatalf("agent %q: %s, want %s", c.agent, got, c.want)
		}
	}
	res := conntest.Call(t, jobsconn.Tools(e.db, func() int64 { return NOW }, "agt_gone"), "list_jobs", `{"includePaused":true}`)
	if res.N("count") != 0 {
		t.Fatalf("%s", res)
	}
}
