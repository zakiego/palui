package t3code_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/connectors/t3code"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

const (
	TOKEN = "t3.session.token"
	CODE  = "PAIR-CODE-1234"
)

type state struct {
	mu         sync.Mutex
	pairs      int
	launches   []map[string]any
	sends      []map[string]any
	interrupts []string
	runs       map[string]string
	pending    float64
	reply      string
}

func fresh() *state {
	return &state{runs: map[string]string{}, reply: "Done: fixed the bug and opened PR #12."}
}

func (s *state) run() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs["run-1"]
}

func (s *state) setRun(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs["run-1"] = v
}

var projects = []any{
	map[string]any{"id": "p1", "title": "palui", "workspaceRoot": "/Users/z/Code/palui", "archivedAt": nil, "deletedAt": nil},
	map[string]any{"id": "p2", "title": "vorneo", "workspaceRoot": "/Users/z/Code/vorneo", "archivedAt": nil, "deletedAt": nil},
	map[string]any{"id": "p3", "title": "old", "workspaceRoot": "/Users/z/Code/old", "archivedAt": "2026-01-01", "deletedAt": nil},
	map[string]any{"id": "p5", "title": "site", "workspaceRoot": "/Users/z/Code/site", "defaultModelSelection": map[string]any{"instanceId": "codex", "model": "gpt"}, "archivedAt": nil, "deletedAt": nil},
}

func failure(code, message string) *sdk.CallToolResult {
	b, _ := json.Marshal(map[string]any{"_tag": "OrchestratorMcpFailure", "code": code, "message": message})
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}
}

func thread(id string, s *state) map[string]any {
	status := s.run()
	if status == "" {
		status = "idle"
	}
	var active any
	if status == "running" {
		active = "run-1"
	}
	s.mu.Lock()
	pending := s.pending
	s.mu.Unlock()
	return map[string]any{"threadId": id, "projectId": "p1", "title": "Fix the bug", "link": "https://app.t3.codes/threads/env1/" + id, "status": status, "latestRunId": "run-1", "activeRunId": active, "branch": "zaki/fix-bug", "pendingRequestCount": pending, "pullRequests": []any{}}
}

func register(s *state) func(*sdk.Server) {
	obj := `{"type":"object"}`
	return func(srv *sdk.Server) {
		conntest.AddRawTool(srv, "t3_project_list", "", obj, nil, func(map[string]any) *sdk.CallToolResult {
			return conntest.JSONResult(map[string]any{"projects": projects, "nextCursor": nil})
		})
		conntest.AddRawTool(srv, "t3_thread_list", "", obj, nil, func(map[string]any) *sdk.CallToolResult {
			return conntest.JSONResult(map[string]any{"threads": []any{thread("11111111-aaaa", s)}, "nextCursor": nil})
		})
		conntest.AddRawTool(srv, "orchestrator_capabilities", "", obj, nil, func(map[string]any) *sdk.CallToolResult {
			return conntest.JSONResult(map[string]any{"providers": []any{
				map[string]any{"providerInstanceId": "codex", "models": []any{}, "constraints": []any{"Provider instance is disabled."}},
				map[string]any{"providerInstanceId": "claudeAgent", "models": []any{map[string]any{"id": "claude-opus-5-5"}, map[string]any{"id": "claude-sonnet-5-5"}}, "constraints": []any{}},
			}})
		})
		conntest.AddRawTool(srv, "t3_thread_launch", "", obj, nil, func(a map[string]any) *sdk.CallToolResult {
			s.mu.Lock()
			s.launches = append(s.launches, a)
			s.runs["run-1"] = "running"
			s.mu.Unlock()
			return conntest.JSONResult(map[string]any{"threadId": "11111111-aaaa", "projectId": a["projectId"], "runId": "run-1", "link": "https://app.t3.codes/threads/env1/11111111-aaaa"})
		})
		conntest.AddRawTool(srv, "t3_thread_send", "", obj, nil, func(a map[string]any) *sdk.CallToolResult {
			s.mu.Lock()
			s.sends = append(s.sends, a)
			s.runs["run-1"] = "running"
			s.mu.Unlock()
			return conntest.JSONResult(map[string]any{"threadId": a["threadId"], "delivered": "started"})
		})
		conntest.AddRawTool(srv, "t3_thread_wait", "", obj, nil, func(a map[string]any) *sdk.CallToolResult {
			id, _ := a["threadId"].(string)
			if id == "gone" {
				return failure("thread_not_found", "Thread "+id+" was not found.")
			}
			runID, _ := a["runId"].(string)
			if runID == "" {
				runID = "run-1"
			}
			s.mu.Lock()
			status := s.runs[runID]
			s.mu.Unlock()
			if status == "" {
				status = "idle"
			}
			return conntest.JSONResult(map[string]any{"threadId": id, "runId": runID, "status": status, "timedOut": status == "running"})
		})
		conntest.AddRawTool(srv, "t3_thread_read", "", obj, nil, func(a map[string]any) *sdk.CallToolResult {
			id, _ := a["threadId"].(string)
			if id == "gone" {
				return failure("thread_not_found", "Thread "+id+" was not found.")
			}
			s.mu.Lock()
			reply := s.reply
			s.mu.Unlock()
			return conntest.JSONResult(map[string]any{"thread": thread(id, s), "items": []any{
				map[string]any{"itemId": "i1", "runId": "run-1", "type": "user_message", "text": "Fix the bug", "position": 0},
				map[string]any{"itemId": "i2", "runId": "run-1", "type": "assistant_message", "text": reply, "position": 1},
			}})
		})
		conntest.AddRawTool(srv, "t3_thread_interrupt", "", obj, nil, func(a map[string]any) *sdk.CallToolResult {
			id, _ := a["threadId"].(string)
			s.mu.Lock()
			s.interrupts = append(s.interrupts, id)
			s.mu.Unlock()
			return conntest.JSONResult(map[string]any{"threadId": id, "interrupted": true})
		})
	}
}

func oauth(s *state) func(w http.ResponseWriter, r *http.Request, body string) bool {
	send := func(w http.ResponseWriter, status int, v any) bool {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
		return true
	}
	return func(w http.ResponseWriter, r *http.Request, body string) bool {
		switch r.URL.Path {
		case "/oauth/mcp/register":
			var b map[string]any
			_ = json.Unmarshal([]byte(body), &b)
			return send(w, 201, map[string]any{"client_id": "client-1", "client_name": b["client_name"]})
		case "/oauth/mcp/decision":
			var b struct {
				Authorization map[string]any `json:"authorization"`
				Decision      map[string]any `json:"decision"`
			}
			_ = json.Unmarshal([]byte(body), &b)
			if b.Decision["code"] != CODE || b.Decision["access"] != "full-access" {
				return send(w, 400, map[string]any{"_tag": "AuthMcpApprovalError", "message": "That pairing code is unknown or used."})
			}
			if !strings.HasSuffix(b.Authorization["resource"].(string), "/mcp") {
				return send(w, 400, map[string]any{"message": "bad resource"})
			}
			return send(w, 200, map[string]any{"redirectTo": b.Authorization["redirect_uri"].(string) + "?code=auth-code&state=" + b.Authorization["state"].(string) + "&iss=x"})
		case "/oauth/mcp/token":
			if r.Header.Get("content-type") != "application/x-www-form-urlencoded" {
				return send(w, 415, map[string]any{"message": "Unsupported content-type"})
			}
			form, _ := url.ParseQuery(body)
			if form.Get("code") != "auth-code" || form.Get("code_verifier") == "" {
				return send(w, 400, map[string]any{"error": "invalid_grant"})
			}
			s.mu.Lock()
			s.pairs++
			s.mu.Unlock()
			return send(w, 200, map[string]any{"access_token": TOKEN, "token_type": "Bearer", "expires_in": 2_592_000, "scope": "orchestration:read"})
		}
		return false
	}
}

func server(t *testing.T, s *state) string {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register(s), Token: TOKEN, Rest: oauth(s)})
	return srv.URL
}

func ctxFor(t *testing.T, rawURL string, secrets map[string]string, config string) *conntest.FakeCtx {
	o, err := jsonx.ParseObject([]byte(`{"url":"` + rawURL + `"` + config + `}`))
	testutil.Must(t, err)
	return conntest.NewCtx(t, nil, "t3code", o, secrets)
}

func exec(t *testing.T, set *connectors.ToolSet, name, input string) conntest.Value {
	t.Helper()
	return conntest.Call(t, set, name, input)
}

func outboxTexts(t *testing.T, d *db.DB) []string {
	rows, err := d.Query("SELECT payload, source FROM outbox ORDER BY rowid")
	testutil.Must(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p, src string
		testutil.Must(t, rows.Scan(&p, &src))
		out = append(out, src+"|"+conntest.Of(json.RawMessage(p)).S("text"))
	}
	return out
}

func TestHelpersParseConfigWithDefaultsAndReadThreadIDsFromLinks(t *testing.T) {
	if c := t3code.ParseConfig(nil); c != (t3code.Config{URL: "http://host.docker.internal:3773", BaseRef: "main"}) {
		t.Fatalf("%+v", c)
	}
	if m := t3code.ParseModel("claudeAgent/claude-opus-5-5"); m == nil || *m != (t3code.Model{InstanceID: "claudeAgent", Model: "claude-opus-5-5"}) {
		t.Fatal(m)
	}
	if t3code.ParseModel("opus") != nil {
		t.Fatal("parsed opus")
	}
	if _, err := t3code.Manifest.ParseConfig(kit.O("model", "opus")); err == nil {
		t.Fatal("accepted opus")
	}
	if out, _ := t3code.Manifest.ParseConfig(nil); jsonx.Stringify(out) != `{"url":"http://host.docker.internal:3773","baseRef":"main"}` {
		t.Fatal(jsonx.Stringify(out))
	}
	cases := map[string]string{
		"https://app.t3.codes/threads/8b457a52-5eaa/b3b33950-a26e-4e45-a9a3-533bfa7efb92": "b3b33950-a26e-4e45-a9a3-533bfa7efb92",
		" b3b33950-a26e ": "b3b33950-a26e",
		"https://app.t3.codes/threads/8b457a52/mcp%3Alaunch%3Aa5c604d2/": "mcp:launch:a5c604d2",
		"mcp:launch:a5c604d2": "mcp:launch:a5c604d2",
	}
	for in, want := range cases {
		if got := t3code.ThreadIDOf(in); got != want {
			t.Fatal(in, got)
		}
	}
}

func decodeProjects(t *testing.T, list []any) []t3code.Project {
	b, _ := json.Marshal(list)
	var out []t3code.Project
	testutil.Must(t, json.Unmarshal(b, &out))
	return out
}

func TestHelpersMatchProjectsByNameFolderIDOrPathAndSkipArchivedOnes(t *testing.T) {
	all := decodeProjects(t, projects)
	var live []t3code.Project
	for _, p := range all {
		if p.ArchivedAt == nil {
			live = append(live, p)
		}
	}
	for q, id := range map[string]string{"Palui": "p1", "p2": "p2", "vor": "p2", "/Users/z/Code/palui": "p1"} {
		if p, why := t3code.MatchProject(live, q); p == nil || p.ID != id {
			t.Fatal(q, why)
		}
	}
	twins := append(append([]t3code.Project(nil), live...), t3code.Project{ID: "p4", Title: "palui-site", WorkspaceRoot: "/Users/z/Code/palui-site"})
	if _, why := t3code.MatchProject(twins, "pal"); !strings.Contains(why, "matches 2 projects") {
		t.Fatal(why)
	}
	if p, _ := t3code.MatchProject(twins, "palui"); p == nil || p.ID != "p1" {
		t.Fatal(p)
	}
	if _, why := t3code.MatchProject(live, "nope"); !strings.Contains(why, "palui, vorneo, site") {
		t.Fatal(why)
	}
}

func TestHelpersWriteTheFinishedMessageInIndonesian(t *testing.T) {
	w := t3code.Watch{ThreadID: "t", ChatID: "c", Title: "Fix bug", Link: "https://app.t3.codes/threads/e/t"}
	if got := t3code.FinishedText(w, "completed", "All good."); got != "T3 Code beres: Fix bug\n\nAll good.\n\nhttps://app.t3.codes/threads/e/t" {
		t.Fatal(got)
	}
	if got := t3code.FinishedText(w, "failed", ""); got != "T3 Code berhenti (failed): Fix bug\n\nhttps://app.t3.codes/threads/e/t" {
		t.Fatal(got)
	}
	if got := t3code.FinishedText(w, "completed", strings.Repeat("x", 5000)); len(got) >= 1600+2 {
		t.Fatal(len(got))
	}
}

func TestPairingTradesAPairingCodeForATokenOnceAndKeepsIt(t *testing.T) {
	s := fresh()
	u := server(t, s)
	res, err := t3code.PairWithCode(context.Background(), u, CODE, nil, 1000)
	testutil.Must(t, err)
	if res.Token != TOKEN || res.ExpiresAt == nil || *res.ExpiresAt != 1000+2_592_000_000 {
		t.Fatalf("%+v", res)
	}
	if _, err := t3code.PairWithCode(context.Background(), u, "WRONG-CODE-1", nil, 1000); err == nil || !strings.Contains(err.Error(), "unknown or used") {
		t.Fatal(err)
	}
	f := ctxFor(t, u, map[string]string{"pairingCode": CODE}, "")
	tok, err := t3code.EnsureToken(f.Context, t3code.ParseConfig(f.Config), nil, 1)
	testutil.Must(t, err)
	if tok != TOKEN || len(f.SecretPatches) != 1 || jsonx.Stringify(f.SecretPatches[0]) != `{"token":"`+TOKEN+`"}` {
		t.Fatal(tok, f.SecretPatches)
	}
	if pc, _ := f.PatchList()[0].Get("pairedCode"); pc != t3code.CodeFingerprint(CODE) {
		t.Fatal(pc)
	}
	tok, err = t3code.EnsureToken(f.Context, t3code.ParseConfig(f.Config), nil, 1)
	testutil.Must(t, err)
	if tok != TOKEN || s.pairs != 2 {
		t.Fatal(s.pairs)
	}
}

func TestTestPairsAndCountsLiveProjectsABadCodeReadsClearly(t *testing.T) {
	s := fresh()
	u := server(t, s)
	ok, err := t3code.Runtime.Test(ctxFor(t, u, map[string]string{"pairingCode": CODE}, "").Context)
	testutil.Must(t, err)
	if !ok.OK || ok.Detail != "Connected to T3 Code: 3 projects" {
		t.Fatalf("%+v", ok)
	}
	bad, _ := t3code.Runtime.Test(ctxFor(t, u, map[string]string{"pairingCode": "WRONG-CODE-1"}, "").Context)
	if bad.OK || !strings.Contains(bad.Detail, "pairing code") {
		t.Fatalf("%+v", bad)
	}
}

func TestARejectedTokenSaysHowToFixIt(t *testing.T) {
	s := fresh()
	u := server(t, s)
	res, _ := t3code.Runtime.Test(ctxFor(t, u, map[string]string{"pairingCode": CODE, "token": "expired"}, `,"pairedCode":"`+t3code.CodeFingerprint(CODE)+`"`).Context)
	if res.OK || !strings.Contains(res.Detail, "t3 auth pairing create") {
		t.Fatalf("%+v", res)
	}
}

type world struct {
	s       *state
	f       *conntest.FakeCtx
	chatID  string
	turn    connectors.TurnInfo
	started *t3code.Started
	tools   *connectors.ToolSet
}

func setup(t *testing.T) *world {
	s := fresh()
	u := server(t, s)
	f := ctxFor(t, u, map[string]string{"pairingCode": CODE, "token": TOKEN}, `,"pairedCode":"`+t3code.CodeFingerprint(CODE)+`"`)
	chat, err := chats.Ensure(f.DB, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, 0)
	testutil.Must(t, err)
	turn := connectors.TurnInfo{RunID: "run_a", ChatID: chat.ID, Channel: "telegram", Trigger: "message", TZ: "Asia/Makassar"}
	started := t3code.Start(f.Context, TOKEN, t3code.StartOptions{PollMs: 3_600_000})
	t.Cleanup(func() { started.Handle.Stop(context.Background()) })
	set, err := started.Handle.Tools(context.Background(), turn)
	testutil.Must(t, err)
	return &world{s, f, chat.ID, turn, started, set}
}

func TestOffersSevenToolsWithTheRightClasses(t *testing.T) {
	w := setup(t)
	if got := strings.Join(conntest.Classes(w.tools), ","); got != "list_projects:read,list_threads:read,start_thread:write,send_to_thread:write,list_chat_threads:read,get_thread:read,stop_thread:destructive" {
		t.Fatal(got)
	}
	for _, k := range w.tools.Keys() {
		if pt, _ := w.tools.Get(k); pt.Untrusted {
			t.Fatal(k)
		}
	}
}

func TestStartsAThreadInAWorktreeThenTellsTheChatWhenItFinishes(t *testing.T) {
	w := setup(t)
	out := exec(t, w.tools, "start_thread", `{"project":"palui","task":"Fix the bug\nin the router"}`)
	if !out.B("ok") || out.S("threadId") != "11111111-aaaa" || out.S("environmentId") != "env1" || out.S("title") != "Fix the bug" || out.S("project") != "palui" || out.S("workspace") != "new worktree from main" {
		t.Fatal(out)
	}
	l := conntest.Of(w.s.launches[0])
	if l.S("projectId") != "p1" || l.S("title") != "Fix the bug" || l.JSON("modelSelection") != `{"instanceId":"claudeAgent","model":"claude-opus-5-5"}` || l.S("workspaceStrategy.type") != "worktree" || l.S("workspaceStrategy.baseRef") != "main" || !strings.HasPrefix(l.S("clientRequestId"), "palui-run_a-") {
		t.Fatal(l)
	}
	if len(w.started.Watcher.List()) != 1 {
		t.Fatal("not watched")
	}
	w.started.Watcher.Poll(context.Background(), 1)
	if len(outboxTexts(t, w.f.DB)) != 0 {
		t.Fatal("notified while running")
	}
	w.s.setRun("completed")
	w.started.Watcher.Poll(context.Background(), 1)
	rows := outboxTexts(t, w.f.DB)
	if len(rows) != 1 || rows[0] != "t3code:11111111-aaaa|T3 Code beres: Fix the bug\n\n"+w.s.reply+"\n\nhttps://app.t3.codes/threads/env1/11111111-aaaa" {
		t.Fatal(rows)
	}
	if len(w.started.Watcher.List()) != 0 {
		t.Fatal("still watched")
	}
	patches := w.f.PatchList()
	if jsonx.Stringify(patches[len(patches)-1]) != `{"watches":[]}` {
		t.Fatal(jsonx.Stringify(patches[len(patches)-1]))
	}
	list := exec(t, w.tools, "list_chat_threads", `{}`)
	if !list.B("ok") || list.S("threads.0.threadId") != "11111111-aaaa" || list.S("threads.0.action") != "started" || list.S("threads.0.environmentId") != "env1" {
		t.Fatal(list)
	}
}

func TestWorksInPlaceWhenAskedAndReportsAnUnknownProject(t *testing.T) {
	w := setup(t)
	exec(t, w.tools, "start_thread", `{"project":"vorneo","task":"Bump deps","inPlace":true}`)
	if _, ok := w.s.launches[0]["workspaceStrategy"]; ok {
		t.Fatal("worktree")
	}
	exec(t, w.tools, "start_thread", `{"project":"site","task":"Fix CSS"}`)
	if _, ok := w.s.launches[1]["modelSelection"]; ok {
		t.Fatal("model set")
	}
	if r := exec(t, w.tools, "start_thread", `{"project":"nope","task":"x"}`); r.B("ok") || !strings.Contains(r.S("error"), "No T3 Code project") {
		t.Fatal(r)
	}
}

func TestSaysOnceWhenAThreadWaitsForAnAnswer(t *testing.T) {
	w := setup(t)
	exec(t, w.tools, "start_thread", `{"project":"palui","task":"Fix it"}`)
	w.s.mu.Lock()
	w.s.pending = 1
	w.s.mu.Unlock()
	w.started.Watcher.Poll(context.Background(), 1)
	w.started.Watcher.Poll(context.Background(), 1)
	rows := outboxTexts(t, w.f.DB)
	if len(rows) != 1 || rows[0] != "t3code:11111111-aaaa|T3 Code nunggu jawaban kamu: Fix it\n\nhttps://app.t3.codes/threads/env1/11111111-aaaa" {
		t.Fatal(rows)
	}
}

func TestSendsFollowUpsReadsAndStopsThreadsByIDOrLink(t *testing.T) {
	w := setup(t)
	link := "https://app.t3.codes/threads/env1/11111111-aaaa"
	r := exec(t, w.tools, "send_to_thread", `{"thread":"`+link+`","message":"Also add a test"}`)
	if !r.B("ok") || r.S("thread.id") != "11111111-aaaa" || r.S("thread.status") != "running" {
		t.Fatal(r)
	}
	sent := conntest.Of(w.s.sends[0])
	if sent.S("threadId") != "11111111-aaaa" || sent.S("message") != "Also add a test" || sent.S("mode") != "auto" {
		t.Fatal(sent)
	}
	if wl := w.started.Watcher.List(); len(wl) != 1 || wl[0].ThreadID != "11111111-aaaa" || wl[0].RunID == nil || *wl[0].RunID != "run-1" {
		t.Fatalf("%+v", wl)
	}
	read := exec(t, w.tools, "get_thread", `{"thread":"11111111-aaaa"}`)
	if strings.Join(read.Strs("messages", "from"), ",") != "owner,agent" {
		t.Fatal(read)
	}
	if r := exec(t, w.tools, "list_threads", `{"project":"palui","activeOnly":true}`); !r.B("ok") || r.S("threads.0.id") != "11111111-aaaa" {
		t.Fatal(r)
	}
	if r := exec(t, w.tools, "list_projects", `{}`); strings.Join(r.Strs("projects", "name"), ",") != "palui,vorneo,site" {
		t.Fatal(r)
	}
	if got := exec(t, w.tools, "stop_thread", `{"thread":"`+link+`"}`).String(); got != `{"ok":true,"threadId":"11111111-aaaa"}` {
		t.Fatal(got)
	}
	if strings.Join(w.s.interrupts, ",") != "11111111-aaaa" {
		t.Fatal(w.s.interrupts)
	}
	if got := exec(t, w.tools, "get_thread", `{"thread":"gone"}`).String(); got != `{"ok":false,"error":"Thread gone was not found."}` {
		t.Fatal(got)
	}
}

func TestListsOnlyTheThreadsStartedOrFollowedFromThisChat(t *testing.T) {
	w := setup(t)
	if got := exec(t, w.tools, "list_chat_threads", `{}`).String(); got != `{"ok":true,"chatId":"`+w.chatID+`","threads":[],"message":"Belum ada thread T3 Code dari chat ini."}` {
		t.Fatal(got)
	}
	exec(t, w.tools, "start_thread", `{"project":"palui","task":"Fix it"}`)
	exec(t, w.tools, "send_to_thread", `{"thread":"11111111-aaaa","message":"More"}`)
	other, err := chats.Ensure(w.f.DB, chats.EnsureInput{Channel: "whatsapp", ExternalID: "628@s.whatsapp.net"}, 0)
	testutil.Must(t, err)
	ot := w.turn
	ot.ChatID = other.ID
	otherTools, err := w.started.Handle.Tools(context.Background(), ot)
	testutil.Must(t, err)
	if r := exec(t, otherTools, "list_chat_threads", `{}`); !r.B("ok") || r.S("chatId") != other.ID || r.Len("threads") != 0 {
		t.Fatal(r)
	}
	if got := exec(t, w.tools, "list_chat_threads", `{}`).JSON("threads"); got != `[{"threadId":"11111111-aaaa","title":"Fix the bug","action":"followed","at":"1970-01-01T00:00:00.000Z","link":"https://app.t3.codes/threads/env1/11111111-aaaa","environmentId":"env1"}]` {
		t.Fatal(got)
	}
	if r := exec(t, w.tools, "list_threads", `{}`); r.S("threads.0.id") != "11111111-aaaa" || r.S("threads.0.environmentId") != "env1" {
		t.Fatal(r)
	}
}

func TestReadsTheEnvironmentIDOutOfAThreadLink(t *testing.T) {
	if got := t3code.EnvironmentIDOf("https://app.t3.codes/threads/8b457a52-1111/mcp%3Alaunch%3Aabc"); got != "8b457a52-1111" {
		t.Fatal(got)
	}
	if t3code.EnvironmentIDOf("https://app.t3.codes/projects/x") != "" || t3code.EnvironmentIDOf("") != "" {
		t.Fatal("found an environment")
	}
}

func TestUsesTheConfiguredModelOverT3CodesFirstOne(t *testing.T) {
	s := fresh()
	u := server(t, s)
	f := ctxFor(t, u, map[string]string{"token": TOKEN}, `,"model":"claudeAgent/claude-sonnet-5-5","baseRef":"develop"`)
	chat, err := chats.Ensure(f.DB, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, 0)
	testutil.Must(t, err)
	started := t3code.Start(f.Context, TOKEN, t3code.StartOptions{PollMs: 3_600_000})
	defer started.Handle.Stop(context.Background())
	set, err := started.Handle.Tools(context.Background(), connectors.TurnInfo{RunID: "r", ChatID: chat.ID, Channel: "telegram", Trigger: "message", TZ: "UTC"})
	testutil.Must(t, err)
	exec(t, set, "start_thread", `{"project":"palui","task":"Do it"}`)
	l := conntest.Of(s.launches[0])
	if l.JSON("modelSelection") != `{"instanceId":"claudeAgent","model":"claude-sonnet-5-5"}` || l.S("workspaceStrategy.baseRef") != "develop" {
		t.Fatal(l)
	}
}

func TestDropsWatchesForMissingThreadsAndAfter24Hours(t *testing.T) {
	s := fresh()
	u := server(t, s)
	now := int64(10 * t3code.WatchTTLMs)
	watches := []any{
		map[string]any{"threadId": "gone", "runId": nil, "chatId": "c", "title": "Gone", "since": now},
		map[string]any{"threadId": "11111111-aaaa", "runId": "run-1", "chatId": "c", "title": "Old", "since": now - t3code.WatchTTLMs - 1},
	}
	b, _ := json.Marshal(watches)
	f := ctxFor(t, u, map[string]string{"token": TOKEN}, `,"watches":`+string(b))
	client := t3code.NewClient(u, TOKEN, nil)
	defer client.Close()
	w := t3code.NewWatcher(f.Context, func() *t3code.Client { return client }, func(error) {})
	if len(w.List()) != 2 {
		t.Fatal(len(w.List()))
	}
	w.Poll(context.Background(), now)
	if len(w.List()) != 0 {
		t.Fatal(w.List())
	}
	if len(f.Events) != 1 || f.Events[0].Kind != "t3code.watch_expired" {
		t.Fatal(f.Events)
	}
}

func TestASharedChatGetsNoT3CodeTool(t *testing.T) {
	w := setup(t)
	entry := connectors.RunningEntry{ID: "t3code", Row: db.Connector{ID: "t3code", Kind: "tools", Label: "T3 Code", Type: "t3code"}, Handle: w.started.Handle}
	outside := w.turn
	outside.Mode = "outside"
	c := tools.Collect(context.Background(), []connectors.RunningEntry{entry}, tools.CollectOptions{Turn: outside, MaxTools: 64, Audience: tools.AudienceOutside})
	if len(c.Names()) != 0 || len(c.ConnectorIDs) != 0 {
		t.Fatal(c.Names())
	}
	if all := tools.Collect(context.Background(), []connectors.RunningEntry{entry}, tools.CollectOptions{Turn: w.turn, MaxTools: 64}); len(all.Names()) != 7 {
		t.Fatal(all.Names())
	}
}
