package rpcsetup_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat/chattest"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/registry"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ported from apps/app/src/server/connectors.test.ts (the action cases; the
// wizard cases live in connectors/manage), run-detail.test.ts, usage.test.ts,
// events-stream.test.ts (statusSummary) and backup.test.ts (latestBackup),
// through the endpoints.

const tgToken = "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq"

// telegramRow is setup(): a telegram connector with its token, not set up.
func telegramRow(t *testing.T, q db.Q) {
	t.Helper()
	m, _ := registry.Manifest("telegram")
	sec := jsonx.NewMap[string]()
	sec.Set("botToken", tgToken)
	_, err := connectors.Upsert(q, connectors.UpsertInput{ID: "telegram", Manifest: m.Row(), Secrets: sec}, testutil.TestKey, 1)
	testutil.Must(t, err)
}

// liveRuntime is the in-process runtime on the server's own database, with a
// fake telegram runtime that counts starts and stops.
func liveRuntime(t *testing.T, d *db.DB) (*httpapi.Runtime, *atomic.Int32) {
	t.Helper()
	var stops atomic.Int32
	m, _ := registry.Manifest("telegram")
	fake := &connectors.Runtime{
		Manifest: connectors.Manifest{ID: "telegram", Kind: "tools", Name: m.Name},
		Test: func(connectors.Context) (connectors.TestResult, error) {
			return connectors.TestResult{OK: true, Detail: "reachable"}, nil
		},
		Start: func(connectors.Context) (*connectors.RunningConnector, error) {
			return &connectors.RunningConnector{Stop: func(context.Context) error { stops.Add(1); return nil }}, nil
		},
	}
	h := chattest.New(t, chattest.Options{DB: d, Registry: map[string]*connectors.Runtime{"telegram": fake}})
	return &httpapi.Runtime{DB: d, Engine: h.Engine, Sup: h.Sup, Bus: h.Bus, Clock: h.Clock.Now}, &stops
}

func row(t *testing.T, q db.Q, id string) (db.Connector, bool) {
	t.Helper()
	r, ok, err := connectors.Get(q, id)
	testutil.Must(t, err)
	return r, ok
}

func eventKinds(t *testing.T, q db.Q) []string {
	t.Helper()
	rows, err := q.Query("SELECT kind FROM events ORDER BY id")
	kinds, err := db.All(rows, err, func(s db.Scanner) (string, error) {
		var k string
		return k, s.Scan(&k)
	})
	testutil.Must(t, err)
	return kinds
}

func count(list []string, v string) int {
	n := 0
	for _, x := range list {
		if x == v {
			n++
		}
	}
	return n
}

func errorOf(t *testing.T, body string) string {
	t.Helper()
	var e struct{ Error string }
	testutil.Must(t, json.Unmarshal([]byte(body), &e))
	return e.Error
}

func TestPauseWritesTheStoreAndPokesTheRuntime(t *testing.T) {
	d := testutil.DB(t)
	telegramRow(t, d)
	testutil.Must(t, connectors.Connect(d, "telegram", 2))
	rt, stops := liveRuntime(t, d)
	if r, _ := row(t, d, "telegram"); r.Status != "on" {
		t.Fatalf("the fake didn't start: %s", r.Status)
	}
	e, token := env(t, d, t.TempDir(), rt, 0, "")
	status, body := e.Post(token, "pauseConnectorFn", `{"id":"telegram"}`)
	if status != 200 || body != `{"ok":true,"runtimeReachable":true}` {
		t.Fatal(status, body)
	}
	if r, _ := row(t, d, "telegram"); r.Enabled || r.Status != "paused" {
		t.Fatalf("%+v", r)
	}
	// The poke reconciled: the supervisor stopped the paused connector.
	conntest.Until(t, func() bool { return stops.Load() == 1 })
	if count(eventKinds(t, d), "pause") != 1 {
		t.Fatal(eventKinds(t, d))
	}
}

func TestPauseAndResumeStillApplyWhenTheRuntimeIsDown(t *testing.T) {
	d := testutil.DB(t)
	telegramRow(t, d)
	testutil.Must(t, connectors.Connect(d, "telegram", 2))
	e, token := env(t, d, t.TempDir(), nil, 0, "")
	if status, body := e.Post(token, "pauseConnectorFn", `{"id":"telegram"}`); status != 200 || body != `{"ok":true,"runtimeReachable":false}` {
		t.Fatal(status, body)
	}
	if r, _ := row(t, d, "telegram"); r.Status != "paused" {
		t.Fatal(r.Status)
	}
	if status, body := e.Post(token, "resumeConnectorFn", `{"id":"telegram"}`); status != 200 || body != `{"ok":true,"runtimeReachable":false}` {
		t.Fatal(status, body)
	}
	if r, _ := row(t, d, "telegram"); !r.Enabled || r.Status != "connecting" {
		t.Fatalf("%+v", r)
	}
}

func TestPauseAndResumeRefuseADraftThatWasNeverConnected(t *testing.T) {
	d := testutil.DB(t)
	telegramRow(t, d)
	e, token := env(t, d, t.TempDir(), fakeRuntime(t, nil), 0, "")
	for _, fn := range []string{"resumeConnectorFn", "pauseConnectorFn"} {
		status, body := e.Post(token, fn, `{"id":"telegram"}`)
		if status != 400 || !strings.Contains(errorOf(t, body), "isn't set up yet") {
			t.Fatal(fn, status, body)
		}
	}
	if r, _ := row(t, d, "telegram"); r.Status != "not_set_up" {
		t.Fatal(r.Status)
	}
}

func TestAnUnknownIDIsAClearError(t *testing.T) {
	d := testutil.DB(t)
	telegramRow(t, d)
	e, token := env(t, d, t.TempDir(), fakeRuntime(t, nil), 0, "")
	status, body := e.Post(token, "pauseConnectorFn", `{"id":"nope"}`)
	if status != 400 || body != `{"error":"No connector \"nope\". Run `+"`palui connectors list`"+` to see them.","code":"BAD_REQUEST"}` {
		t.Fatal(status, body)
	}
}

func TestRemoveGoesThroughTheRuntimeWhenItsUp(t *testing.T) {
	d := testutil.DB(t)
	telegramRow(t, d)
	testutil.Must(t, connectors.Connect(d, "telegram", 2))
	rt, stops := liveRuntime(t, d)
	e, token := env(t, d, t.TempDir(), rt, 0, "")
	status, body := e.Post(token, "removeConnectorFn", `{"id":"telegram"}`)
	if status != 200 || body != `{"ok":true,"runtimeReachable":true}` {
		t.Fatal(status, body)
	}
	if _, ok := row(t, d, "telegram"); ok {
		t.Fatal("still there")
	}
	// The runtime stopped and deleted it; the endpoint didn't delete it twice.
	if stops.Load() != 1 || count(eventKinds(t, d), "remove") != 1 {
		t.Fatal(stops.Load(), eventKinds(t, d))
	}
}

func TestRemoveDeletesCredentialsLocallyWhenTheRuntimeIsDownAndSaysSo(t *testing.T) {
	d := testutil.DB(t)
	telegramRow(t, d)
	e, token := env(t, d, t.TempDir(), nil, 0, "")
	status, body := e.Post(token, "removeConnectorFn", `{"id":"telegram"}`)
	var res struct {
		OK, RuntimeReachable bool
		Detail               string
	}
	testutil.Must(t, json.Unmarshal([]byte(body), &res))
	if status != 200 || !res.OK || res.RuntimeReachable || !strings.Contains(res.Detail, "runtime was down") {
		t.Fatal(status, body)
	}
	if _, ok := row(t, d, "telegram"); ok {
		t.Fatal("still there")
	}
}

func TestRemoveSurfacesARuntimeRefusalInsteadOfDeleting(t *testing.T) {
	d := testutil.DB(t)
	telegramRow(t, d)
	// The fake runtime has no such connector, so it refuses.
	e, token := env(t, d, t.TempDir(), fakeRuntime(t, nil), 0, "")
	status, body := e.Post(token, "removeConnectorFn", `{"id":"telegram"}`)
	if status != 400 || errorOf(t, body) != "No connector telegram" {
		t.Fatal(status, body)
	}
	if _, ok := row(t, d, "telegram"); !ok {
		t.Fatal("deleted")
	}
}

func TestTheWizardSavesADraftAndListsSecretsMasked(t *testing.T) {
	d := testutil.DB(t)
	e, token := env(t, d, t.TempDir(), nil, 0, "")
	status, body := e.Post(token, "saveConnectorDraft", `{"id":"telegram","type":"telegram","secrets":{"botToken":"nope"}}`)
	if status != 200 || !strings.Contains(body, `"ok":false`) || !strings.Contains(body, `"botToken":"That doesn't look like a bot token`) {
		t.Fatal(status, body)
	}
	if status, body := e.Post(token, "saveConnectorDraft", `{"id":"telegram","type":"telegram","secrets":{"botToken":"`+tgToken+`"}}`); status != 200 || body != `{"ok":true}` {
		t.Fatal(status, body)
	}
	if r, _ := row(t, d, "telegram"); r.Status != "not_set_up" {
		t.Fatal(r.Status)
	}
	_, body = e.Get(token, "getConnectorsPage", "")
	if !strings.Contains(body, `"secrets":{"botToken":"••••ppqq"}`) || !strings.Contains(body, `"runtimeUp":false`) {
		t.Fatal(body)
	}
}

// run-detail.test.ts

const runNow int64 = 1_762_800_000_000

func runSeed(t *testing.T, d *db.DB) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := d.Exec(sql, args...)
		testutil.Must(t, err)
	}
	exec("INSERT INTO chats (id, channel, external_id, kind, title, created_at) VALUES ('chat_1', 'web', 'web', 'dm', 'Palui (web)', ?)", runNow)
	exec("INSERT INTO runs (id, chat_id, trigger, model, tokens_in, tokens_out, tokens_cached, tokens_cache_write, steps, status, started_at, ms) VALUES ('run_1', 'chat_1', 'message', 'm', 1000, 20, 800, 150, 2, 'ok', ?, 900)", runNow)
	for _, m := range []struct {
		id, role, content, text, run string
		at                           int64
	}{
		{"msg_1", "user", `{"text":"ingatkan besok"}`, "ingatkan besok", "run_1", runNow},
		{"msg_2", "assistant", `[]`, "Siap, besok 09.00", "run_1", runNow + 2},
		{"msg_3", "assistant", `[]`, "", "run_1", runNow + 3},
		{"msg_4", "user", `{"text":"other"}`, "other", "run_2", runNow + 4},
	} {
		exec("INSERT INTO messages (id, chat_id, role, content, text, run_id, created_at) VALUES (?, 'chat_1', ?, ?, ?, ?, ?)", m.id, m.role, m.content, m.text, m.run, m.at)
	}
	exec("INSERT INTO tool_calls (id, run_id, tool, class, args, result_preview, status, ms, created_at) VALUES ('tc_2', 'run_1', 'reminders__set_reminder', 'write', ?, 'ok', 'ok', 80, ?)", `{"when":"besok"}`, runNow+1)
	exec("INSERT INTO tool_calls (id, run_id, tool, class, args, result_preview, status, ms, created_at) VALUES ('tc_1', 'run_1', 'recall__cari', 'read', NULL, NULL, 'error', NULL, ?)", runNow)
}

type runPage struct {
	Run *struct {
		ID, Chat               string
		ChatID                 *string
		ChatGone               bool
		TokensCached           int64
		TokensCacheWrite       int64
		TokensCounted, Steps   int64
		Prompt, Reply          []struct{ Text string }
		Tools                  []struct{ Tool, Args string }
		Job                    *string
		JobID                  *string
		JobGone, Tainted       bool
		ResultPreview, Trigger *string
	}
	FlowRun any
}

func getRunPage(t *testing.T, e *apitest.Env, token, id string) runPage {
	t.Helper()
	status, body := e.Get(token, "getRun", `{"id":"`+id+`"}`)
	if status != 200 {
		t.Fatal(status, body)
	}
	var p runPage
	testutil.Must(t, json.Unmarshal([]byte(body), &p))
	return p
}

func TestRunDetailReturnsTheRunWithItsPromptReplyAndToolCallsInOrder(t *testing.T) {
	d := testutil.DB(t)
	runSeed(t, d)
	e, token := env(t, d, t.TempDir(), nil, 0, "")
	r := getRunPage(t, e, token, "run_1").Run
	if r == nil || r.ID != "run_1" || r.Chat != "Palui (web)" || r.ChatID == nil || *r.ChatID != "chat_1" || r.ChatGone || r.TokensCached != 800 || r.TokensCacheWrite != 150 || r.Steps != 2 {
		t.Fatalf("%+v", r)
	}
	// 50 fresh + 150 written + 80 for the 800 read + 20 out.
	if r.TokensCounted != 300 {
		t.Fatal(r.TokensCounted)
	}
	if len(r.Prompt) != 1 || r.Prompt[0].Text != "ingatkan besok" {
		t.Fatalf("%+v", r.Prompt)
	}
	// Empty assistant messages (a turn that only called tools) are left out.
	if len(r.Reply) != 1 || r.Reply[0].Text != "Siap, besok 09.00" {
		t.Fatalf("%+v", r.Reply)
	}
	if len(r.Tools) != 2 || r.Tools[0].Tool != "recall__cari" || r.Tools[1].Tool != "reminders__set_reminder" {
		t.Fatalf("%+v", r.Tools)
	}
	if r.Tools[0].Args != "" || !strings.Contains(r.Tools[1].Args, `"when": "besok"`) {
		t.Fatalf("%+v", r.Tools)
	}
}

func TestRunDetailSaysTheChatIsGoneInsteadOfLinkingToIt(t *testing.T) {
	d := testutil.DB(t)
	runSeed(t, d)
	_, err := d.Exec("INSERT INTO runs (id, chat_id, trigger, status, started_at) VALUES ('run_3', 'chat_gone', 'message', 'error', ?)", runNow)
	testutil.Must(t, err)
	e, token := env(t, d, t.TempDir(), nil, 0, "")
	_, body := e.Get(token, "getRun", `{"id":"run_3"}`)
	for _, want := range []string{`"chatId":null`, `"chat":null`, `"chatGone":true`, `"prompt":[]`, `"reply":[]`, `"tools":[]`} {
		if !strings.Contains(body, want) {
			t.Fatal(want, body)
		}
	}
}

func TestRunDetailReturnsNullForAnUnknownID(t *testing.T) {
	d := testutil.DB(t)
	runSeed(t, d)
	e, token := env(t, d, t.TempDir(), nil, 0, "")
	if p := getRunPage(t, e, token, "run_nope"); p.Run != nil || p.FlowRun != nil {
		t.Fatalf("%+v", p)
	}
}

// usage.test.ts

func TestUsageSplitsTokensPerAgentWithOldRunsUnderTheDefaultAgentAndDeletedOnesGrouped(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", "UTC", 1))
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := d.Exec(sql, args...)
		testutil.Must(t, err)
	}
	exec("INSERT INTO chats (id, channel, external_id, kind, title, created_at) VALUES ('chat_1', 'web', 'web', 'dm', 'Web', ?)", runNow)
	exec("INSERT INTO agents (id, slug, name, persona, is_default, created_at, updated_at) VALUES ('agt_palui', 'palui', 'Palui', '', 1, ?, ?), ('agt_cs', 'cs', 'CS', '', 0, ?, ?)", runNow, runNow, runNow, runNow)
	for _, r := range []struct {
		id    string
		agent any
		in    int64
	}{{"run_1", "agt_cs", 500}, {"run_2", "agt_cs", 300}, {"run_3", "agt_palui", 100}, {"run_4", nil, 40}, {"run_5", "agt_gone", 20}, {"run_6", "agt_gone2", 30}} {
		exec("INSERT INTO runs (id, chat_id, agent_id, trigger, model, tokens_in, tokens_out, status, started_at) VALUES (?, 'chat_1', ?, 'message', 'm', ?, 10, 'ok', ?)", r.id, r.agent, r.in, runNow-1000)
	}
	e, token := env(t, d, t.TempDir(), nil, runNow, "")
	_, body := e.Get(token, "getUsage", "")
	var u struct {
		PerAgent struct {
			Rows []struct {
				Key          string
				Tokens, Runs int64
			}
			Total int
		}
	}
	testutil.Must(t, json.Unmarshal([]byte(body), &u))
	got, _ := json.Marshal(u.PerAgent)
	if string(got) != `{"Rows":[{"Key":"CS","Tokens":820,"Runs":2},{"Key":"Palui","Tokens":160,"Runs":2},{"Key":"","Tokens":70,"Runs":2}],"Total":3}` {
		t.Fatal(string(got))
	}
}

// events-stream.test.ts statusSummary

func TestStatusSummaryIgnoresDraftsAndChosenPauses(t *testing.T) {
	d := testutil.DB(t)
	m, _ := registry.Manifest("telegram")
	on, off := true, false
	_, err := connectors.Upsert(d, connectors.UpsertInput{ID: "draft", Manifest: m.Row()}, testutil.TestKey, 1)
	testutil.Must(t, err)
	_, err = connectors.Upsert(d, connectors.UpsertInput{ID: "telegram", Manifest: m.Row(), Enabled: &on}, testutil.TestKey, 1)
	testutil.Must(t, err)
	_, err = connectors.Upsert(d, connectors.UpsertInput{ID: "cliproxy", Manifest: connectors.RowManifest{ID: "cliproxy", Kind: "model", Name: "CLIProxyAPI"}, Enabled: &off}, testutil.TestKey, 1)
	testutil.Must(t, err)
	testutil.Must(t, connectors.SetStatus(d, "telegram", "on", nil, 2))
	testutil.Must(t, connectors.SetStatus(d, "cliproxy", "paused", nil, 2))
	e, token := env(t, d, t.TempDir(), nil, 0, "")
	if _, body := e.Get(token, "getStatusSummary", ""); body != `{"total":2,"on":1,"attention":[]}` {
		t.Fatal(body)
	}
	detail := "401 from Telegram"
	testutil.Must(t, connectors.SetStatus(d, "telegram", "error", &detail, 3))
	if _, body := e.Get(token, "getStatusSummary", ""); body != `{"total":2,"on":0,"attention":[{"id":"telegram","label":"Telegram","status":"error"}]}` {
		t.Fatal(body)
	}
}

// backup.test.ts latestBackup, through the Settings page.

func TestLatestBackupIgnoresHalfWrittenTempFiles(t *testing.T) {
	d := testutil.DB(t)
	dir := t.TempDir()
	writeBackup(t, dir, "pre-deploy-20261109T000000Z.db.gz", 10, 1_762_646_400)
	writeBackup(t, dir, "pre-deploy-20261110T000000Z.db.gz.tmp-123", 10, 1_762_732_800)
	e, token := env(t, d, dir, nil, 0, "")
	_, body := e.Get(token, "getSettingsPage", "")
	if !strings.Contains(body, `"backup":{"file":"pre-deploy-20261109T000000Z.db.gz","bytes":10,"at":1762646400000}`) {
		t.Fatal(body)
	}
}
