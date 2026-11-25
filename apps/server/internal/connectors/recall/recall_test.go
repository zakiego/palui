package recall_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/recall"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const (
	KEY            = "rcl_testkey123456"
	SERVER_DEFAULT = "zaki"
)

var turn = connectors.TurnInfo{RunID: "run_1", ChatID: "chat_1", Channel: "telegram", Trigger: "message", TZ: "Asia/Makassar"}

type seen struct {
	Method, Path, Auth string
	Body               map[string]any
}

type fakeOpts struct {
	profileDelay time.Duration
	profile      map[string]any
	profileDown  bool
	// What /v1/containers lists; nil is default and zaki.
	containers func() []string
	// How long /v1/containers takes to answer.
	containersDelay time.Duration
	// Tool results: a tool named here answers with this text, as an error when errors[tool].
	results map[string]string
	errors  map[string]bool
	// The runtime's health and container timeout; 0 is recall.Runtime's 5 s.
	timeoutMs int64
}

type fake struct {
	srv      *conntest.MCPServer
	base     string
	mu       sync.Mutex
	seen     []seen
	toolArgs []map[string]any
}

func (f *fake) Seen() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen(nil), f.seen...)
}

func (f *fake) Args() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.toolArgs...)
}

func (f *fake) paths(filter func(seen) bool) []string {
	var out []string
	for _, s := range f.Seen() {
		if filter(s) {
			out = append(out, s.Path)
		}
	}
	return out
}

// waitOrDone waits d, and says whether the request was dropped meanwhile.
func waitOrDone(r *http.Request, d time.Duration) bool {
	select {
	case <-time.After(d):
		return false
	case <-r.Context().Done():
		return true
	}
}

func deletes(s seen) bool { return s.Method == "DELETE" }

func fakeRecall(t *testing.T, o fakeOpts) *fake {
	f := &fake{}
	record := func(tool string) func(map[string]any) *sdk.CallToolResult {
		return func(a map[string]any) *sdk.CallToolResult {
			f.mu.Lock()
			rec := map[string]any{"tool": tool}
			for k, v := range a {
				rec[k] = v
			}
			f.toolArgs = append(f.toolArgs, rec)
			f.mu.Unlock()
			if text, ok := o.results[tool]; ok {
				r := conntest.TextResult(text)
				r.IsError = o.errors[tool]
				return r
			}
			return conntest.TextResult(tool + " ok")
		}
	}
	register := func(s *sdk.Server) {
		conntest.AddRawTool(s, "add_memory", "", `{"type":"object","properties":{"content":{"type":"string"},"container":{"type":"string"}}}`, &sdk.ToolAnnotations{DestructiveHint: func() *bool { b := false; return &b }()}, record("add_memory"))
		for _, name := range []string{"search_memories", "ask_memory", "get_profile", "list_memories"} {
			conntest.AddRawTool(s, name, "", `{"type":"object","properties":{"query":{"type":"string"},"container":{"type":"string"}}}`, &sdk.ToolAnnotations{ReadOnlyHint: true}, record(name))
		}
		conntest.AddRawTool(s, "purge_container", "", `{"type":"object","properties":{"container":{"type":"string"}}}`, nil, func(map[string]any) *sdk.CallToolResult { return conntest.TextResult("purged") })
	}
	send := func(w http.ResponseWriter, status int, body any) bool {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
		return true
	}
	f.srv = conntest.StartMCPServer(t, conntest.MCPOptions{Token: KEY, Register: register, Rest: func(w http.ResponseWriter, r *http.Request, raw string) bool {
		var body map[string]any
		if raw != "" {
			_ = json.Unmarshal([]byte(raw), &body)
		}
		path := r.URL.Path
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		f.mu.Lock()
		f.seen = append(f.seen, seen{r.Method, path, r.Header.Get("authorization"), body})
		f.mu.Unlock()
		if r.URL.Path == "/health" {
			return send(w, 200, map[string]any{"ok": true, "vec": "v0.1"})
		}
		if r.Header.Get("authorization") != "Bearer "+KEY {
			return send(w, 401, map[string]any{"error": map[string]any{"type": "unauthorized", "message": "missing or invalid API key"}})
		}
		switch {
		case r.URL.Path == "/v1/containers" && o.containersDelay > 0 && waitOrDone(r, o.containersDelay):
			return true
		case r.URL.Path == "/v1/containers" && o.containers != nil:
			list := []any{}
			for _, c := range o.containers() {
				list = append(list, map[string]any{"container": c, "memories": 1, "documents": 0, "lastActivity": 1})
			}
			return send(w, 200, map[string]any{"containers": list})
		case r.URL.Path == "/v1/containers":
			return send(w, 200, map[string]any{"containers": []any{map[string]any{"container": "default", "memories": 3, "documents": 1, "lastActivity": 1}, map[string]any{"container": "zaki", "memories": 9, "documents": 2, "lastActivity": 2}}})
		case r.URL.Path == "/v1/profile":
			if o.profileDelay > 0 {
				select {
				case <-time.After(o.profileDelay):
				case <-r.Context().Done():
					return true
				}
			}
			if o.profileDown {
				return send(w, 500, map[string]any{"error": map[string]any{"type": "internal", "message": "boom"}})
			}
			if o.profile != nil {
				return send(w, 200, o.profile)
			}
			c := r.URL.Query().Get("container")
			if c == "" {
				c = SERVER_DEFAULT
			}
			return send(w, 200, map[string]any{"container": c, "static": "Zaki lives in Banjarbaru.", "dynamic": "Building Palui.", "updatedAt": 1, "staticMemories": []any{}, "dynamicMemories": []any{}, "searchResults": []any{map[string]any{"type": "memory", "id": "m1", "text": "Likes kopi susu", "score": 0.9}}})
		case r.URL.Path == "/v1/memories" && r.Method == "POST":
			c, _ := body["container"].(string)
			if c == "" {
				c = SERVER_DEFAULT
			}
			return send(w, 202, map[string]any{"id": "doc_1", "container": c, "status": "queued", "sourceId": body["source_id"], "duplicate": false})
		case strings.HasPrefix(r.URL.Path, "/v1/documents/"):
			id, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/documents/"))
			home := map[string]string{"doc_1": "zaki", "doc_2": "agent-chef"}[id]
			if home == "" {
				return send(w, 404, map[string]any{"error": map[string]any{"type": "not_found", "message": "no such document"}})
			}
			if r.Method == "DELETE" {
				return send(w, 200, map[string]any{"deleted": true})
			}
			return send(w, 200, map[string]any{"document": map[string]any{"id": id, "container": home, "title": "Old doc"}})
		case strings.HasPrefix(r.URL.Path, "/v1/memories/"):
			id, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/memories/"))
			home := map[string]string{"mem_1": "zaki", "mem_2": "agent-chef"}[id]
			if home == "" {
				return send(w, 404, map[string]any{"error": map[string]any{"type": "not_found", "message": "no such memory"}})
			}
			return send(w, 200, map[string]any{"memory": map[string]any{"id": id, "fact": "Old fact", "container": home}})
		}
		return false
	}})
	f.base = strings.TrimSuffix(f.srv.URL, "/mcp")
	return f
}

func cfg(t *testing.T, base, extra string) *jsonx.Object {
	o, err := jsonx.ParseObject([]byte(`{"url":"` + base + `","container":"zaki"` + extra + `}`))
	testutil.Must(t, err)
	out, err := recall.Manifest.ParseConfig(o)
	testutil.Must(t, err)
	return out
}

// savedWithoutContainer is a config as the dashboard stores it when no
// container was picked: the runtime gets it as is, without defaults.
func savedWithoutContainer(t *testing.T, base string) *jsonx.Object {
	o, err := jsonx.ParseObject([]byte(`{"url":"` + base + `","autoRecall":true,"autoSave":true}`))
	testutil.Must(t, err)
	return o
}

func start(t *testing.T, c *jsonx.Object) (*connectors.RunningConnector, *conntest.FakeCtx) {
	t.Helper()
	f := conntest.NewCtx(t, nil, "recall", c, map[string]string{"apiKey": KEY})
	running, err := recall.Runtime.Start(f.Context)
	testutil.Must(t, err)
	t.Cleanup(func() { running.Stop(context.Background()) })
	return running, f
}

func toolsFor(t *testing.T, r *connectors.RunningConnector, turn connectors.TurnInfo) *connectors.ToolSet {
	t.Helper()
	set, err := r.Tools(context.Background(), turn)
	testutil.Must(t, err)
	return set
}

func exec(t *testing.T, set *connectors.ToolSet, name, input string) conntest.Value {
	t.Helper()
	return conntest.Call(t, set, name, input)
}

func argsJSON(list []map[string]any) string {
	b, _ := json.Marshal(list)
	return string(b)
}

func scope(container string, read []string) *connectors.MemoryScope {
	var c *string
	if container != "" {
		c = &container
	}
	return &connectors.MemoryScope{Container: c, Read: read, Locked: true}
}

func TestManifestAppliesDefaults(t *testing.T) {
	out, err := recall.Manifest.ParseConfig(nil)
	testutil.Must(t, err)
	if got := jsonx.Stringify(out); got != `{"url":"http://host.docker.internal:4800","container":"default","autoRecall":true,"autoSave":true}` {
		t.Fatal(got)
	}
	ok := func(k string) bool {
		m := jsonx.NewMap[string]()
		m.Set("apiKey", k)
		return recall.Manifest.ParseSecrets(m) == nil
	}
	if !ok("rcl_abc") || ok("sk-abc") {
		t.Fatal("key check")
	}
}

func TestTestChecksHealthThenListsContainers(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	res, err := recall.Runtime.Test(conntest.Ctx(t, nil, "recall", cfg(t, f.base, ""), map[string]string{"apiKey": KEY}))
	testutil.Must(t, err)
	if !res.OK || res.Detail != "Connected, 2 containers" || strings.Join(conntest.Of(res.Data).Strs("containers", "container"), ",") != "default,zaki" {
		t.Fatalf("%+v", res)
	}
	if got := strings.Join(f.paths(func(seen) bool { return true }), ","); got != "/health,/v1/containers" {
		t.Fatal(got)
	}
}

func TestTestExplainsABadKeyAndAnUnreachableServer(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	res, _ := recall.Runtime.Test(conntest.Ctx(t, nil, "recall", cfg(t, f.base, ""), map[string]string{"apiKey": "rcl_wrong"}))
	if res.OK || res.Detail != "recall rejected the API key (401)" {
		t.Fatalf("%+v", res)
	}
	down, _ := recall.Runtime.Test(conntest.Ctx(t, nil, "recall", cfg(t, "http://127.0.0.1:1", ""), map[string]string{"apiKey": KEY}))
	if down.OK || !strings.Contains(down.Detail, "Can't reach recall") {
		t.Fatalf("%+v", down)
	}
}

func TestClassesMCPToolsAddsForgetAndLocksATurnWithoutAScope(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, cfg(t, f.base, ""))
	set := toolsFor(t, running, turn)
	// The forget tools come first, then the server's tools in the order it
	// lists them (the Go SDK test server sorts by name; recall's keeps its own).
	classes := conntest.Classes(set)
	rest := append([]string(nil), classes[2:]...)
	sort.Strings(rest)
	if got := strings.Join(append(classes[:2:2], rest...), ","); got != "forget_memory:destructive,forget_document:destructive,add_memory:write,ask_memory:read,get_profile:read,list_memories:read,search_memories:read" {
		t.Fatal(got)
	}
	for _, k := range set.Keys() {
		if pt, _ := set.Get(k); pt.Untrusted {
			t.Fatal(k)
		}
	}
	exec(t, set, "search_memories", `{"query":"kopi"}`)
	if r := exec(t, set, "add_memory", `{"content":"x","container":"other"}`); r.Err == nil || !strings.Contains(r.Err.Error(), "can't use recall container other") {
		t.Fatal(r.Err)
	}
	if got := argsJSON(f.Args()); got != `[{"container":"zaki","query":"kopi","tool":"search_memories"}]` {
		t.Fatal(got)
	}
	if got := exec(t, set, "forget_memory", `{"id":"mem_1"}`).String(); got != `{"forgotten":true,"id":"mem_1","fact":"Old fact"}` {
		t.Fatal(got)
	}
	if exec(t, set, "forget_memory", `{"id":"mem_2"}`).B("forgotten") || exec(t, set, "forget_memory", `{"id":"nope"}`).B("forgotten") {
		t.Fatal("forgot another container's memory")
	}
	if got := strings.Join(f.paths(deletes), ","); got != "/v1/memories/mem_1" {
		t.Fatal(got)
	}
	res, err := running.Action(context.Background(), "listContainers", nil)
	testutil.Must(t, err)
	if strings.Join(conntest.Of(res).Strs("containers", "container"), ",") != "default,zaki" {
		t.Fatal(res)
	}
	// The Memory tab reads plain JSON values, not the API structs.
	v, _ := res.Get("containers")
	items, _ := v.([]any)
	first, ok := items[0].(*jsonx.Object)
	if !ok {
		t.Fatalf("container item is %T, want *jsonx.Object", items[0])
	}
	if m, _ := first.Get("memories"); m != 3.0 {
		t.Fatalf("memories is %T %v, want float64 3", m, m)
	}
	if got := jsonx.Stringify(first); got != `{"container":"default","memories":3,"documents":1,"lastActivity":1}` {
		t.Fatal(got)
	}
}

func TestLocksPaluiToTheOwnersContainerAndItsSharedList(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, cfg(t, f.base, ""))
	tt := turn
	tt.AgentID, tt.Memory = "agt_palui", scope("", []string{"kb-home"})
	set := toolsFor(t, running, tt)
	if _, ok := set.Get("purge_container"); ok {
		t.Fatal("unknown tool offered")
	}
	exec(t, set, "search_memories", `{"query":"laundry"}`)
	exec(t, set, "search_memories", `{"query":"laundry","container":"kb-home"}`)
	if r := exec(t, set, "search_memories", `{"query":"laundry","container":"agent-finansial"}`); r.Err == nil || !strings.Contains(r.Err.Error(), "can't use recall container agent-finansial") {
		t.Fatal(r.Err)
	}
	if r := exec(t, set, "add_memory", `{"content":"x","container":"agent-finansial"}`); r.Err == nil || !strings.Contains(r.Err.Error(), "can write zaki") {
		t.Fatal(r.Err)
	}
	if got := argsJSON(f.Args()); got != `[{"container":"zaki","query":"laundry","tool":"search_memories"},{"container":"kb-home","query":"laundry","tool":"search_memories"}]` {
		t.Fatal(got)
	}
	if exec(t, set, "forget_memory", `{"id":"mem_2"}`).B("forgotten") || len(f.paths(deletes)) != 0 {
		t.Fatal("deleted")
	}
}

func TestForgetMemoryTakesManyIDsInOneCallAndStillChecksEachContainer(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, cfg(t, f.base, ""))
	tt := turn
	tt.Memory = scope("zaki", []string{})
	set := toolsFor(t, running, tt)
	if got := exec(t, set, "forget_memory", `{"ids":["mem_1","mem_2","nope","mem_1"]}`).String(); got != `{"forgotten":1,"results":[{"forgotten":true,"id":"mem_1","fact":"Old fact"},{"forgotten":false,"id":"mem_2","error":"No memory with that id"},{"forgotten":false,"id":"nope","error":"No memory with that id"}]}` {
		t.Fatal(got)
	}
	if got := strings.Join(f.paths(deletes), ","); got != "/v1/memories/mem_1" {
		t.Fatal(got)
	}
	if got := exec(t, set, "forget_memory", `{"id":"mem_1"}`).String(); got != `{"forgotten":true,"id":"mem_1","fact":"Old fact"}` {
		t.Fatal(got)
	}
	if r := exec(t, set, "forget_memory", `{}`); r.N("forgotten") != 0 || r.S("error") != "Pass ids." {
		t.Fatal(r)
	}
}

func TestForgetDocumentDeletesOnlyADocumentInTheTurnsOwnContainer(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, cfg(t, f.base, ""))
	tt := turn
	tt.Memory = scope("zaki", []string{})
	set := toolsFor(t, running, tt)
	cases := map[string]string{
		"doc_1": `{"deleted":true,"id":"doc_1","title":"Old doc"}`,
		"doc_2": `{"deleted":false,"id":"doc_2","error":"No document with that id"}`,
		"doc_9": `{"deleted":false,"id":"doc_9","error":"No document with that id"}`,
	}
	for _, id := range []string{"doc_1", "doc_2", "doc_9"} {
		if got := exec(t, set, "forget_document", `{"id":"`+id+`"}`).String(); got != cases[id] {
			t.Fatal(id, got)
		}
	}
	if got := strings.Join(f.paths(deletes), ","); got != "/v1/documents/doc_1" {
		t.Fatal(got)
	}
}

func TestLocksAnAgentsToolsToItsOwnContainerAndItsSharedOnes(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, cfg(t, f.base, ""))
	tt := turn
	tt.AgentID, tt.Memory = "agt_chef", scope("agent-chef", []string{"kb-menu"})
	set := toolsFor(t, running, tt)
	keys := set.Keys()
	sort.Strings(keys)
	if strings.Join(keys, ",") != "add_memory,ask_memory,forget_document,forget_memory,get_profile,list_memories,search_memories" {
		t.Fatal(keys)
	}
	exec(t, set, "search_memories", `{"query":"sambal"}`)
	exec(t, set, "search_memories", `{"query":"sambal","container":"kb-menu"}`)
	exec(t, set, "add_memory", `{"content":"x"}`)
	if r := exec(t, set, "search_memories", `{"query":"x","container":"zaki"}`); r.Err == nil || !strings.Contains(r.Err.Error(), "can't use recall container zaki") {
		t.Fatal(r.Err)
	}
	if r := exec(t, set, "add_memory", `{"content":"x","container":"kb-menu"}`); r.Err == nil || !strings.Contains(r.Err.Error(), "can write agent-chef") {
		t.Fatal(r.Err)
	}
	if got := argsJSON(f.Args()); got != `[{"container":"agent-chef","query":"sambal","tool":"search_memories"},{"container":"kb-menu","query":"sambal","tool":"search_memories"},{"container":"agent-chef","content":"x","tool":"add_memory"}]` {
		t.Fatal(got)
	}
	if exec(t, set, "forget_memory", `{"id":"mem_1"}`).B("forgotten") || !exec(t, set, "forget_memory", `{"id":"mem_2"}`).B("forgotten") {
		t.Fatal("wrong container")
	}
	if got := strings.Join(f.paths(deletes), ","); got != "/v1/memories/mem_2" {
		t.Fatal(got)
	}
}

func TestRecallReturnsTheProfileTextForTheConfiguredContainer(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, cfg(t, f.base, ""))
	text, ok, err := running.Memory.Recall(context.Background(), "what do I usually drink in the morning", connectors.RecallOptions{TimeoutMs: 1500})
	testutil.Must(t, err)
	if !ok || !strings.Contains(text, "About the owner: Zaki lives in Banjarbaru.") || !strings.Contains(text, "- Likes kopi susu") {
		t.Fatal(text)
	}
	profiles := f.paths(func(s seen) bool { return strings.HasPrefix(s.Path, "/v1/profile") })
	if len(profiles) != 1 || profiles[0] != "/v1/profile?container=zaki&q=what+do+I+usually+drink+in+the+morning" {
		t.Fatal(profiles)
	}
	for _, q := range []string{"/status", "halo"} {
		if _, ok, _ := running.Memory.Recall(context.Background(), q, connectors.RecallOptions{TimeoutMs: 1500}); ok {
			t.Fatal(q)
		}
	}
	if n := len(f.paths(func(s seen) bool { return strings.HasPrefix(s.Path, "/v1/profile") })); n != 1 {
		t.Fatal(n)
	}
	live := conntest.Of(running.Live())
	if live.N("recall.calls") != 1 || live.N("recall.hits") != 1 || live.N("recall.skippedShort") != 2 || live.N("recall.skipRate") != 0 {
		t.Fatal(live)
	}
}

func TestRecallGivesUpWithNullOnTimeout(t *testing.T) {
	f := fakeRecall(t, fakeOpts{profileDelay: 500 * time.Millisecond})
	running, _ := start(t, cfg(t, f.base, ""))
	started := time.Now()
	if _, ok, _ := running.Memory.Recall(context.Background(), "tell me about my plans for this week", connectors.RecallOptions{TimeoutMs: 50}); ok {
		t.Fatal("got a profile")
	}
	if time.Since(started) > 400*time.Millisecond {
		t.Fatal(time.Since(started))
	}
	live := conntest.Of(running.Live())
	if live.N("recall.calls") != 1 || live.N("recall.timeouts") != 1 || live.N("recall.skipRate") != 1 {
		t.Fatal(live)
	}
}

func TestSavePostsToMemoriesWithTheContainerAndSourceID(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, cfg(t, f.base, `,"autoSave":false`))
	if running.Memory.AutoSave || !running.Memory.AutoRecall {
		t.Fatal("flags")
	}
	testutil.Must(t, running.Memory.Save(context.Background(), connectors.SaveInput{SourceID: "ses_1:0-40", Content: "Zaki: aku suka kopi\nPalui: noted", Title: "Chat 10 Nov"}))
	testutil.Must(t, running.Memory.Save(context.Background(), connectors.SaveInput{SourceID: "ses_1:41-50", Content: "   "}))
	var posts []seen
	for _, s := range f.Seen() {
		if s.Method == "POST" {
			posts = append(posts, s)
		}
	}
	b, _ := json.Marshal(posts[0].Body)
	if len(posts) != 1 || string(b) != `{"container":"zaki","content":"Zaki: aku suka kopi\nPalui: noted","source_id":"ses_1:0-40","title":"Chat 10 Nov"}` {
		t.Fatal(len(posts), string(b))
	}
}

// The setup form only sends url and the key, and the runtime later learns
// defaultContainer: that stored shape keeps auto-recall and auto-save on.
func TestAutoRecallAndAutoSaveDefaultOnWhenTheConfigLeavesThemOut(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	o, err := jsonx.ParseObject([]byte(`{"url":"` + f.base + `","defaultContainer":"zaki"}`))
	testutil.Must(t, err)
	running, _ := start(t, o)
	if !running.Memory.AutoRecall || !running.Memory.AutoSave {
		t.Fatal("flags", running.Memory.AutoRecall, running.Memory.AutoSave)
	}
	live := conntest.Of(running.Live())
	if live.Get("autoRecall") != true || live.Get("autoSave") != true {
		t.Fatal(live)
	}
}

func TestStartFailsWhenRecallIsUnreachable(t *testing.T) {
	_, err := recall.Runtime.Start(conntest.Ctx(t, nil, "recall", cfg(t, "http://127.0.0.1:1", ""), map[string]string{"apiKey": KEY}))
	if err == nil || !strings.Contains(err.Error(), "Can't reach recall") {
		t.Fatal(err)
	}
}

var palui = func() connectors.TurnInfo {
	tt := turn
	tt.AgentID, tt.Memory = "agt_palui", scope("", []string{})
	return tt
}()

func isDefaultProfile(s seen) bool { return s.Path == "/v1/profile" }

func TestDefaultContainerAddForgetAndForgetDocumentUseTheSameContainer(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, savedWithoutContainer(t, f.base))
	set := toolsFor(t, running, palui)
	exec(t, set, "add_memory", `{"content":"Kopi favorit Zaki adalah kopi susu"}`)
	exec(t, set, "search_memories", `{"query":"kopi susu"}`)
	if got := argsJSON(f.Args()); got != `[{"container":"zaki","content":"Kopi favorit Zaki adalah kopi susu","tool":"add_memory"},{"container":"zaki","query":"kopi susu","tool":"search_memories"}]` {
		t.Fatal(got)
	}
	if got := exec(t, set, "forget_memory", `{"ids":["mem_1"]}`).String(); got != `{"forgotten":1,"results":[{"forgotten":true,"id":"mem_1","fact":"Old fact"}]}` {
		t.Fatal(got)
	}
	if got := exec(t, set, "forget_document", `{"id":"doc_1"}`).String(); got != `{"deleted":true,"id":"doc_1","title":"Old doc"}` {
		t.Fatal(got)
	}
	if got := strings.Join(f.paths(deletes), ","); got != "/v1/memories/mem_1,/v1/documents/doc_1" {
		t.Fatal(got)
	}
	toolsFor(t, running, palui)
	if n := len(f.paths(isDefaultProfile)); n != 1 {
		t.Fatal(n)
	}
}

func TestDefaultContainerStillRefusesAnotherAgentsIDs(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, savedWithoutContainer(t, f.base))
	set := toolsFor(t, running, palui)
	if got := exec(t, set, "forget_memory", `{"ids":["mem_2"]}`).String(); got != `{"forgotten":0,"results":[{"forgotten":false,"id":"mem_2","error":"No memory with that id"}]}` {
		t.Fatal(got)
	}
	if got := exec(t, set, "forget_document", `{"id":"doc_2"}`).String(); got != `{"deleted":false,"id":"doc_2","error":"No document with that id"}` {
		t.Fatal(got)
	}
	chef := turn
	chef.AgentID, chef.Memory = "agt_chef", scope("agent-chef", []string{})
	cset := toolsFor(t, running, chef)
	if got := exec(t, cset, "forget_memory", `{"ids":["mem_1"]}`).String(); got != `{"forgotten":0,"results":[{"forgotten":false,"id":"mem_1","error":"No memory with that id"}]}` {
		t.Fatal(got)
	}
	if got := exec(t, cset, "forget_document", `{"id":"doc_1"}`).String(); got != `{"deleted":false,"id":"doc_1","error":"No document with that id"}` {
		t.Fatal(got)
	}
	if len(f.paths(deletes)) != 0 {
		t.Fatal("deleted")
	}
}

func TestDefaultContainerNamesTheContainersAnAgentMayUseWhenItRefusesOne(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, savedWithoutContainer(t, f.base))
	set := toolsFor(t, running, palui)
	if r := exec(t, set, "search_memories", `{"query":"x","container":"agent-finansial"}`); r.Err == nil || r.Err.Error() != "This agent can't use recall container agent-finansial. It can read zaki." {
		t.Fatal(r.Err)
	}
	if r := exec(t, set, "add_memory", `{"content":"x","container":"agent-finansial"}`); r.Err == nil || !strings.Contains(r.Err.Error(), "It can write zaki.") {
		t.Fatal(r.Err)
	}
}

func TestDefaultContainerSavesRecallsDefaultNameInTheConfig(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, fc := start(t, savedWithoutContainer(t, f.base))
	toolsFor(t, running, palui)
	patches := fc.PatchList()
	if len(patches) != 1 || jsonx.Stringify(patches[0]) != `{"defaultContainer":"zaki"}` || len(f.paths(isDefaultProfile)) != 1 {
		t.Fatal(patches)
	}
	g := fakeRecall(t, fakeOpts{})
	picked, pc := start(t, cfg(t, g.base, ""))
	toolsFor(t, picked, turn)
	if len(pc.PatchList()) != 0 || len(g.paths(isDefaultProfile)) != 0 {
		t.Fatal("looked up a picked container")
	}
}

func TestDefaultContainerForgetToolsRefuseWhileRecallWontSayItsDefault(t *testing.T) {
	f := fakeRecall(t, fakeOpts{profileDown: true})
	running, _ := start(t, savedWithoutContainer(t, f.base))
	set := toolsFor(t, running, palui)
	unknown := regexp.MustCompile(`Can't tell which recall container`)
	if r := exec(t, set, "forget_memory", `{"ids":["mem_1"]}`); r.N("forgotten") != 0 || r.B("results.0.forgotten") || !unknown.MatchString(r.S("results.0.error")) {
		t.Fatal(r)
	}
	if r := exec(t, set, "forget_document", `{"id":"doc_1"}`); r.B("deleted") || !unknown.MatchString(r.S("error")) {
		t.Fatal(r)
	}
	if len(f.paths(deletes)) != 0 {
		t.Fatal("deleted")
	}
	if r := exec(t, set, "search_memories", `{"query":"x","container":"agent-finansial"}`); r.Err == nil || !strings.Contains(r.Err.Error(), "It can only use its own container.") {
		t.Fatal(r.Err)
	}
}

func TestDefaultContainerAutoRecallAndAutoSaveSendNoContainer(t *testing.T) {
	f := fakeRecall(t, fakeOpts{})
	running, _ := start(t, savedWithoutContainer(t, f.base))
	running.Memory.Recall(context.Background(), "what do I usually drink in the morning", connectors.RecallOptions{TimeoutMs: 1500})
	testutil.Must(t, running.Memory.Save(context.Background(), connectors.SaveInput{SourceID: "s:1", Content: "Likes kopi susu"}))
	profiles := f.paths(func(s seen) bool { return strings.HasPrefix(s.Path, "/v1/profile") })
	found := false
	for _, p := range profiles {
		if p == "/v1/profile?q=what+do+I+usually+drink+in+the+morning" {
			found = true
		}
		if strings.Contains(p, "undefined") {
			t.Fatal(p)
		}
	}
	if !found {
		t.Fatal(profiles)
	}
	for _, s := range f.Seen() {
		if s.Method == "POST" && s.Path == "/v1/memories" {
			b, _ := json.Marshal(s.Body)
			if string(b) != `{"content":"Likes kopi susu","source_id":"s:1"}` {
				t.Fatal(string(b))
			}
		}
	}
}

func TestHelpersResolveContainer(t *testing.T) {
	locked := recall.Scope{Container: "agent-chef", Read: []string{"kb-menu"}, Locked: true}
	if c, _ := recall.ResolveContainer(nil, "read", locked); c != "agent-chef" {
		t.Fatal(c)
	}
	if c, _ := recall.ResolveContainer(" ", "write", locked); c != "agent-chef" {
		t.Fatal(c)
	}
	if c, _ := recall.ResolveContainer("kb-menu", "read", locked); c != "kb-menu" {
		t.Fatal(c)
	}
	if _, err := recall.ResolveContainer("kb-menu", "write", locked); err == nil {
		t.Fatal("write to a read container")
	}
	if _, err := recall.ResolveContainer("zaki", "read", locked); err == nil {
		t.Fatal("read the owner's")
	}
	if c, _ := recall.ResolveContainer("anything", "write", recall.Scope{Container: "zaki", Locked: false}); c != "anything" {
		t.Fatal(c)
	}
}

func TestHelpersShouldRecall(t *testing.T) {
	if recall.ShouldRecall("/new") || recall.ShouldRecall("apa kabar") || !recall.ShouldRecall("ingat nggak aku suka apa") {
		t.Fatal("shouldRecall")
	}
}

func TestHelpersProfileText(t *testing.T) {
	if _, ok := recall.ProfileText(recall.Profile{Container: "c"}, ""); ok {
		t.Fatal("empty profile")
	}
	text, ok := recall.ProfileText(recall.Profile{Container: "c", StaticMemories: []recall.Memory{{ID: "1", Fact: "Has a cat"}}}, "")
	if !ok || text != "Facts about the owner:\n- Has a cat" {
		t.Fatal(text)
	}
}

func TestHelpersNormalizeSourceID(t *testing.T) {
	if recall.NormalizeSourceID("ses_1:0-40") != "ses_1:0-40" {
		t.Fatal("short id")
	}
	long := recall.NormalizeSourceID(strings.Repeat("x", 300))
	if !regexp.MustCompile(`^palui:[0-9a-f]{64}$`).MatchString(long) || len(long) > 200 {
		t.Fatal(long)
	}
}

func TestHelpersAPIStripsTrailingSlashes(t *testing.T) {
	if got := recall.NewAPI("http://r:4800///", KEY, nil).MCPURL(); got != "http://r:4800/mcp" {
		t.Fatal(got)
	}
}
