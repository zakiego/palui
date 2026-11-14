package flows_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	flowsconn "github.com/zakiego/palui/apps/server/internal/connectors/flows"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Tuesday 2026-11-10 10:00 WITA.
const (
	NOW  = int64(1794276000000)
	AT11 = NOW + 3600_000
	WITA = "Asia/Makassar"
)

const BRIEFING = `{
  "name": "Briefing pagi",
  "triggers": [{ "id": "main", "kind": "schedule", "start": "prayer", "source": { "kind": "times", "times": [{ "time": "06:00", "label": "Briefing" }] } }],
  "steps": [
    { "id": "prayer", "kind": "tool", "tool": "mcp-jadwal-sholat__get_prayer_times", "args": { "city": "Banjarmasin", "date": "{{now.date}}" }, "onError": "continue" },
    { "id": "weather", "kind": "tool", "tool": "webfetch__fetch_page", "args": { "url": "https://wttr.in/Banjarmasin?format=j1" }, "parse": { "field": "text", "as": "json" }, "onError": "continue" },
    { "id": "reminders", "kind": "tool", "tool": "reminders__list_reminders", "args": { "filter": "upcoming", "limit": 20 } },
    { "id": "today", "kind": "set", "values": { "count": "count(steps.reminders.data.reminders, \"date\", now.date)" } },
    {
      "id": "send",
      "kind": "message",
      "text": "Selamat pagi! {{now.date | date}}\n\nSholat: Subuh {{steps.prayer.data.times.fajr | default(\"-\")}}, Zuhur {{steps.prayer.data.times.dhuhr | default(\"-\")}}, Asar {{steps.prayer.data.times.asr | default(\"-\")}}, Magrib {{steps.prayer.data.times.maghrib | default(\"-\")}}, Isya {{steps.prayer.data.times.isha | default(\"-\")}}\nCuaca: {{steps.weather.data.current_condition.0.temp_C | default(\"?\")}}°C, {{steps.weather.data.current_condition.0.weatherDesc.0.value | default(\"nggak kebaca\")}}\n\nPengingat hari ini ({{vars.count}}):\n{{steps.reminders.data.reminders | lines(\"text\")}}"
    }
  ]
}`

type fakeTool struct {
	class, approval string
	untrusted       bool
	run             func(args *jsonx.Object) any
}

type fakeConnector struct {
	id, typ, trust string
	names          []string
	tools          map[string]fakeTool
}

var exampleTools = [][6]string{
	{"mcp-jadwal-sholat", "get_prayer_times", "read", "true", "mcp", ""},
	{"webfetch", "fetch_page", "read", "true", "webfetch", ""},
	{"reminders", "list_reminders", "read", "false", "reminders", ""},
	{"reminders", "set_reminder", "write", "false", "reminders", ""},
	{"reminders", "cancel_reminder", "destructive", "false", "reminders", ""},
	{"mcp-github", "get_commit_status", "read", "true", "mcp", ""},
	{"mcp-github", "add_comment", "write", "true", "mcp", "every_run"},
	{"mcp-keuangan", "list_bills", "read", "false", "mcp", ""},
	{"mcp-keuangan", "mark_paid", "write", "false", "mcp", ""},
	{"notes", "save_note", "write", "false", "notes", ""},
	{"flows", "recap", "read", "false", "flows", ""},
}

func js(s string) any {
	v, err := jsonx.Parse([]byte(s))
	if err != nil {
		panic(err)
	}
	return v
}

func exampleEntries(over map[string]func(*jsonx.Object) any) []connectors.RunningEntry {
	var order []*fakeConnector
	by := map[string]*fakeConnector{}
	for _, t := range exampleTools {
		c := by[t[0]]
		if c == nil {
			trust := "own"
			if t[3] == "true" && t[4] == "mcp" {
				trust = "external"
			}
			c = &fakeConnector{id: t[0], typ: t[4], trust: trust, tools: map[string]fakeTool{}}
			by[t[0]] = c
			order = append(order, c)
		}
		run := func(*jsonx.Object) any { return js(`{"ok":true}`) }
		if r, ok := over[t[0]+"__"+t[1]]; ok {
			run = r
		}
		c.names = append(c.names, t[1])
		c.tools[t[1]] = fakeTool{class: t[2], untrusted: t[3] == "true", approval: t[5], run: run}
	}
	var out []connectors.RunningEntry
	for _, c := range order {
		c := c
		out = append(out, connectors.RunningEntry{
			ID:  c.id,
			Row: db.Connector{ID: c.id, Type: c.typ, Kind: "tools", Label: c.id, Trust: c.trust},
			Handle: &connectors.RunningConnector{Tools: func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
				set := connectors.NewToolSet()
				for _, name := range c.names {
					ft := c.tools[name]
					set.Set(name, connectors.PaluiTool{Class: ft.class, Untrusted: ft.untrusted, FlowApproval: ft.approval, Tool: connectors.Tool{
						Description: name,
						Execute: func(_ context.Context, input json.RawMessage, _ connectors.ToolCallInfo) (any, error) {
							args, err := jsonx.ParseObject(input)
							if err != nil {
								return nil, err
							}
							return ft.run(args), nil
						},
					}})
				}
				return set, nil
			}},
		})
	}
	return out
}

func prayer(*jsonx.Object) any {
	return js(`{"content":[{"type":"text","text":"{\"times\":{\"fajr\":\"04:39\",\"dhuhr\":\"12:01\",\"asr\":\"15:26\",\"maghrib\":\"18:12\",\"isha\":\"19:24\"}}"}]}`)
}

type env struct {
	db     *db.DB
	chatID string
	turn   connectors.TurnInfo
	tools  *connectors.ToolSet
}

func setup(t *testing.T) env {
	d := testutil.DB(t)
	chat, err := chats.EnsureWebChat(d, NOW)
	testutil.Must(t, err)
	turn := connectors.TurnInfo{RunID: "run_1", ChatID: chat.ID, Channel: "web", Trigger: "message", TZ: WITA, Now: NOW}
	entries := exampleEntries(map[string]func(*jsonx.Object) any{
		"mcp-jadwal-sholat__get_prayer_times": prayer,
		"reminders__list_reminders":           func(*jsonx.Object) any { return js(`{"reminders":[]}`) },
	})
	caller := flows.NewConnectorFlowCaller(func() []connectors.RunningEntry { return entries })
	return env{d, chat.ID, turn, flowsconn.Tools(d, turn, flowsconn.Deps{Clock: func() int64 { return NOW }, Tools: caller})}
}

func call(t *testing.T, set *connectors.ToolSet, name, input string) conntest.Value {
	t.Helper()
	return conntest.Call(t, set, name, input)
}

func TestManifestIsABuiltinSkill(t *testing.T) {
	m := flowsconn.Manifest
	if m.ID != "flows" || m.Kind != "skill" || !m.Builtin || m.Icon != "workflow" {
		t.Fatalf("%+v", m)
	}
}

func TestToolsAreClassedLikeTheRFCTable(t *testing.T) {
	e := setup(t)
	want := "create_flow:write,update_flow:write,check_flow:read,test_flow:read,list_flows:read,get_flow:read,pause_flow:write,delete_flow:destructive,run_flow:write,flow_history:read,get_run:read,cancel_run:write,answer_checkin:write,explain_flow:read"
	if got := strings.Join(conntest.Classes(e.tools), ","); got != want {
		t.Fatal(got)
	}
	if pt, _ := e.tools.Get("test_flow"); !pt.Untrusted {
		t.Fatal("test_flow trusted")
	}
}

func TestAreOnlyOfferedInTheOwnersOwnChatsAndOwnerTurns(t *testing.T) {
	e := setup(t)
	allowed := true
	group, err := chats.Ensure(e.db, chats.EnsureInput{Channel: "telegram", ExternalID: "-100", Kind: "group", Allowed: &allowed}, NOW)
	testutil.Must(t, err)
	base := connectors.TurnInfo{RunID: "r", ChatID: e.chatID, Channel: "web", Trigger: "message", TZ: WITA, Now: NOW}
	cases := []struct {
		mut  func(*connectors.TurnInfo)
		want bool
	}{
		{func(*connectors.TurnInfo) {}, true},
		{func(x *connectors.TurnInfo) { x.Trigger = "schedule" }, false},
		{func(x *connectors.TurnInfo) { x.Trigger = "flow" }, false},
		{func(x *connectors.TurnInfo) { x.Trigger = "hook" }, false},
		{func(x *connectors.TurnInfo) { x.ChatID, x.Channel = group.ID, "telegram" }, false},
	}
	for i, c := range cases {
		turn := base
		c.mut(&turn)
		got, err := flowsconn.OfferFlowTools(e.db, turn)
		testutil.Must(t, err)
		if got != c.want {
			t.Fatal(i)
		}
	}
}

func TestChecksDryRunsThenCreatesAFlowAndShowsItsNextTimes(t *testing.T) {
	e := setup(t)
	checked := call(t, e.tools, "check_flow", `{"definition":`+BRIEFING+`}`)
	if !checked.B("ok") || checked.JSON("problems") != "[]" || checked.B("needsApproval") {
		t.Fatal(checked)
	}
	if got := checked.JSON("tools.weather"); got != `{"tool":"webfetch__fetch_page","class":"read","untrusted":true,"flowApproval":"version"}` {
		t.Fatal(got)
	}
	dry := call(t, e.tools, "test_flow", `{"definition":`+BRIEFING+`}`)
	if !dry.B("ok") {
		t.Fatal(dry)
	}
	var steps []string
	for _, s := range dry.List("steps") {
		v := conntest.Value{V: s}
		steps = append(steps, v.S("step")+":"+v.S("status"))
	}
	if strings.Join(steps, ",") != "prayer:ok,weather:ok,reminders:ok,today:ok,send:stubbed" {
		t.Fatal(steps)
	}
	made := call(t, e.tools, "create_flow", `{"definition":`+BRIEFING+`}`)
	if !made.B("ok") || made.N("version") != 1 || made.B("needsApproval") || made.S("message") != `Flow "Briefing pagi" dibuat.` {
		t.Fatal(made)
	}
	if made.S("next.0") != "Besok, Rabu 11 Nov, 06.00 WITA (main: Briefing)" {
		t.Fatal(made.S("next.0"))
	}
	f, err := flows.GetFlow(e.db, made.S("id"))
	testutil.Must(t, err)
	if f.Name != "Briefing pagi" {
		t.Fatal(f.Name)
	}
	dup := call(t, e.tools, "create_flow", `{"definition":`+BRIEFING+`}`)
	if dup.B("ok") || !strings.Contains(dup.S("error"), "already exists") {
		t.Fatal(dup)
	}
}

func TestFindsFlowsRecapForADraftsToolStep(t *testing.T) {
	c := conntest.Ctx(t, nil, "flows", nil, nil)
	chat, err := chats.EnsureWebChat(c.DB, NOW)
	testutil.Must(t, err)
	turn := connectors.TurnInfo{RunID: "run_1", ChatID: chat.ID, Channel: "web", Trigger: "message", TZ: WITA, Now: NOW}
	running, err := flowsconn.Runtime.Start(c)
	testutil.Must(t, err)
	entries := []connectors.RunningEntry{{ID: "flows", Row: db.Connector{ID: "flows", Type: "flows", Kind: "skill", Label: "flows", Trust: "own"}, Handle: running}}
	set := flowsconn.Tools(c.DB, turn, flowsconn.Deps{Clock: func() int64 { return NOW }, Tools: flows.NewConnectorFlowCaller(func() []connectors.RunningEntry { return entries })})
	recap := `{"name":"Rekap","triggers":[{"id":"main","kind":"schedule","start":"recap","source":{"kind":"times","times":[{"time":"20:00"}]}}],"steps":[{"id":"recap","kind":"tool","tool":"flows__recap","args":{"flow":"self","days":7}},{"id":"send","kind":"message","text":"{{steps.recap.data.text}}"}]}`
	r := call(t, set, "check_flow", `{"definition":`+recap+`}`)
	if !r.B("ok") || r.JSON("problems") != "[]" || r.S("tools.recap.tool") != "flows__recap" || r.S("tools.recap.class") != "read" {
		t.Fatal(r)
	}
	testutil.Must(t, running.Stop(context.Background()))
}

func TestPointsNInAReAskToRunFollowUps(t *testing.T) {
	e := setup(t)
	def := func(text string) string {
		return `{"definition":{"name":"Air","triggers":[{"id":"main","kind":"schedule","start":"ask","source":{"kind":"times","times":[{"time":"11:00"}]}}],"steps":[{"id":"ask","kind":"checkin","text":"Udah?","deadline":{"kind":"after","minutes":60},"followUp":{"everyMin":20,"max":2,"text":"` + text + `"}}]}}`
	}
	bad := call(t, e.tools, "check_flow", def("Pengingat ke-{{n}}"))
	if !strings.Contains(bad.S("problems.0.message"), "run.followUps") {
		t.Fatal(bad)
	}
	good := call(t, e.tools, "check_flow", def("Pengingat ke-{{run.followUps}}"))
	if !good.B("ok") || good.JSON("problems") != "[]" {
		t.Fatal(good)
	}
}

func TestRefusesADestructiveToolWithTheProblemAndItsPath(t *testing.T) {
	e := setup(t)
	r := call(t, e.tools, "create_flow", `{"definition":{"name":"Hapus","steps":[{"id":"x","kind":"tool","tool":"reminders__cancel_reminder"}]}}`)
	if r.B("ok") || r.S("problems.0.path") != "steps[0].tool" || !regexp.MustCompile(`destructive. Flows never run destructive tools`).MatchString(r.S("problems.0.message")) {
		t.Fatal(r)
	}
}

func TestPatchesOneStepByIDIntoANewVersionAndANoOpEditChangesNothing(t *testing.T) {
	e := setup(t)
	made := call(t, e.tools, "create_flow", `{"definition":`+BRIEFING+`}`)
	r := call(t, e.tools, "update_flow", `{"id":"briefing pagi","patch":{"steps":{"send":{"text":"Pagi! {{now.date}}"}}},"note":"shorter"}`)
	if !r.B("ok") || r.N("version") != 2 || r.JSON("diff") != `["~ step send"]` {
		t.Fatal(r)
	}
	versions, err := flows.ListVersions(e.db, made.S("id"))
	testutil.Must(t, err)
	if len(versions) != 2 || versions[0].N != 2 || versions[0].Note == nil || *versions[0].Note != "shorter" || versions[1].N != 1 || versions[1].Note != nil {
		t.Fatalf("%+v", versions)
	}
	noop := call(t, e.tools, "update_flow", `{"id":"`+made.S("id")+`","patch":{"steps":{"send":{"text":"Pagi! {{now.date}}"}}}}`)
	if !noop.B("ok") || noop.S("message") != "Nggak ada yang berubah." {
		t.Fatal(noop)
	}
}

func TestAsksForApprovalWhenAWriteToolIsAdded(t *testing.T) {
	e := setup(t)
	r := call(t, e.tools, "create_flow", `{"definition":{"name":"Catat","steps":[{"id":"n","kind":"tool","tool":"notes__save_note","args":{"text":"{{now.date}}"}}]}}`)
	if !r.B("ok") || !r.B("needsApproval") || r.JSON("writeCalls") != `["notes.save_note(text: {{now.date}})"]` {
		t.Fatal(r)
	}
	var payload string
	testutil.Must(t, e.db.QueryRow("SELECT payload FROM outbox ORDER BY rowid LIMIT 1").Scan(&payload))
	if id := conntest.Of(json.RawMessage(payload)).S("actions.0.id"); !strings.HasPrefix(id, "flowver:approve:flv_") {
		t.Fatal(payload)
	}
}

func TestRunsListsShowsPausesAndDeletes(t *testing.T) {
	e := setup(t)
	made := call(t, e.tools, "create_flow", `{"definition":`+BRIEFING+`}`)
	id := made.S("id")
	ran := call(t, e.tools, "run_flow", `{"id":"`+id+`","args":{"x":1}}`)
	if !ran.B("ok") || !strings.HasPrefix(ran.S("runId"), "flr_") {
		t.Fatal(ran)
	}
	list := call(t, e.tools, "list_flows", `{}`)
	if list.S("flows.0.name") != "Briefing pagi" || !list.B("flows.0.active") || list.S("flows.0.approval") != "not_needed" || list.S("flows.0.lastRun.status") != "queued" {
		t.Fatal(list)
	}
	got := call(t, e.tools, "get_flow", `{"id":"`+id+`"}`)
	if got.N("flow.shownVersion") != 1 || got.N("flow.versions.0.n") != 1 || !got.B("flow.versions.0.current") {
		t.Fatal(got)
	}
	hist := call(t, e.tools, "flow_history", `{}`)
	if hist.N("summary.total") != 1 || hist.N("summary.byStatus.queued") != 1 {
		t.Fatal(hist)
	}
	run := call(t, e.tools, "get_run", `{"runId":"`+ran.S("runId")+`"}`)
	if run.S("run.id") != ran.S("runId") || run.S("run.status") != "queued" || run.JSON("run.triggerPayload") != `{"kind":"manual","args":{"x":1}}` {
		t.Fatal(run)
	}
	if !call(t, e.tools, "cancel_run", `{"runId":"`+ran.S("runId")+`"}`).B("ok") {
		t.Fatal("cancel")
	}
	paused := call(t, e.tools, "pause_flow", `{"id":"`+id+`","paused":true,"until":"2026-11-12T08:00"}`)
	if !paused.B("ok") || paused.S("message") != `Flow "Briefing pagi" dijeda sampai Kamis 12 Nov, 08.00 WITA.` {
		t.Fatal(paused)
	}
	if r := call(t, e.tools, "run_flow", `{"id":"`+id+`"}`); r.B("ok") || r.S("error") != `Flow "Briefing pagi" lagi dijeda.` {
		t.Fatal(r)
	}
	if !call(t, e.tools, "delete_flow", `{"id":"`+id+`"}`).B("ok") {
		t.Fatal("delete")
	}
	rows, err := flows.ListFlows(e.db, "")
	testutil.Must(t, err)
	if len(rows) != 0 {
		t.Fatal(len(rows))
	}
}

const CHECKIN = `{"name":"Air","triggers":[{"id":"main","kind":"schedule","start":"ask","source":{"kind":"times","times":[{"time":"11:00","label":"siang"}]}}],"steps":[{"id":"ask","kind":"checkin","text":"Udah minum air {{trigger.label}}?","deadline":{"kind":"after","minutes":60},"followUp":{"everyMin":20,"max":1}}]}`

func withRun(t *testing.T) (env, string) {
	e := setup(t)
	made := call(t, e.tools, "create_flow", `{"definition":`+CHECKIN+`}`)
	engine := flows.NewEngine(flows.EngineOptions{DB: e.db, Log: logx.Silent, Clock: func() int64 { return AT11 }, Serial: true})
	_, err := engine.Sweep(AT11)
	testutil.Must(t, err)
	engine.Idle()
	return e, made.S("id")
}

func TestAnswerCheckinAnswersTheNewestOpenFlowCheckin(t *testing.T) {
	e, id := withRun(t)
	r := call(t, e.tools, "answer_checkin", `{"answer":"udah"}`)
	if !r.B("ok") || r.S("flow") != "Air" || !r.B("closed") || r.S("message") != "Sip, dicatat: Sudah (siang)." {
		t.Fatal(r)
	}
	if call(t, e.tools, "flow_history", `{"id":"`+id+`"}`).N("summary.total") != 1 {
		t.Fatal("history")
	}
}

func TestRecapReturnsCountsAndTheRecapText(t *testing.T) {
	e, id := withRun(t)
	call(t, e.tools, "answer_checkin", `{"answer":"done"}`)
	pt := flowsconn.RecapTool(e.db, e.turn, func() int64 { return AT11 + 60_000 })
	r := conntest.Exec(t, pt.Tool, `{"flow":"`+id+`","days":7}`)
	if !r.B("ok") || r.S("flow") != "Air" || r.N("total") != 1 || r.N("byAnswer.done") != 1 || r.N("missed") != 0 {
		t.Fatal(r)
	}
	if r.S("text") != "📊 Rekap Air, 7 hari terakhir:\n- Sudah: 1\n- Terlewat: 0\nSelesai 1/1." {
		t.Fatal(r.S("text"))
	}
	if conntest.Exec(t, flowsconn.RecapTool(e.db, e.turn, nil).Tool, `{}`).B("ok") {
		t.Fatal("self outside a flow")
	}
	inFlow := e.turn
	inFlow.Trigger, inFlow.FlowID = "flow", id
	if r := conntest.Exec(t, flowsconn.RecapTool(e.db, inFlow, nil).Tool, `{"flow":"self"}`); !r.B("ok") || r.S("flow") != "Air" {
		t.Fatal(r)
	}
}

func TestExplainFlowExplainsARunAndAQuietMoment(t *testing.T) {
	e, id := withRun(t)
	r := call(t, e.tools, "explain_flow", `{"id":"`+id+`","at":"2026-11-10T11:00"}`)
	if !regexp.MustCompile(`^It ran at Hari ini, 11\.00 WITA and is waiting \(checkin\) at step ask until Hari ini, 11\.20 WITA\.$`).MatchString(r.S("verdict")) {
		t.Fatal(r.S("verdict"))
	}
	quiet := call(t, e.tools, "explain_flow", `{"id":"`+id+`","at":"2026-11-10T03:00"}`)
	var facts []string
	for _, f := range quiet.List("facts") {
		facts = append(facts, f.(string))
	}
	if !regexp.MustCompile(`Trigger main occurrences around then: .*11\.00 WITA \(siang\)`).MatchString(strings.Join(facts, " ")) {
		t.Fatal(facts)
	}
	call(t, e.tools, "pause_flow", `{"id":"`+id+`","paused":true}`)
	if got := call(t, e.tools, "explain_flow", `{"id":"`+id+`"}`).S("verdict"); got != `Flow "Air" is paused.` {
		t.Fatal(got)
	}
}
