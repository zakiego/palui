package mcp_test

import (
	"context"
	"encoding/base64"
	"sort"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/attachments"
	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const TOKEN = "zkg_test_token_123456"

var turn = connectors.TurnInfo{RunID: "run_1", ChatID: "chat_1", Channel: "telegram", Trigger: "message", TZ: "Asia/Makassar"}

func boolp(b bool) *bool { return &b }

func register(s *sdk.Server) {
	conntest.AddRawTool(s, "search", "Search things", `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`, &sdk.ToolAnnotations{ReadOnlyHint: true}, func(a map[string]any) *sdk.CallToolResult {
		return conntest.TextResult("found " + a["q"].(string))
	})
	conntest.AddRawTool(s, "add_note", "Add a note", `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`, &sdk.ToolAnnotations{DestructiveHint: boolp(false)}, func(a map[string]any) *sdk.CallToolResult {
		return conntest.TextResult("saved " + a["text"].(string))
	})
	conntest.AddRawTool(s, "delete_all", "Delete everything", "", &sdk.ToolAnnotations{DestructiveHint: boolp(true)}, func(map[string]any) *sdk.CallToolResult { return conntest.TextResult("gone") })
	conntest.AddRawTool(s, "mystery", "No hints", "", nil, func(map[string]any) *sdk.CallToolResult { return conntest.TextResult("?") })
}

func config_(t *testing.T, url string, extra string) *jsonx.Object {
	t.Helper()
	raw := `{"url":"` + url + `","authMode":"bearer"` + extra + `}`
	o, err := jsonx.ParseObject([]byte(raw))
	testutil.Must(t, err)
	out, err := mcpconn.Manifest.ParseConfig(o)
	testutil.Must(t, err)
	return out
}

func ctx(t *testing.T, cfg *jsonx.Object, secrets map[string]string, trust string) *conntest.FakeCtx {
	f := conntest.NewCtx(t, nil, "test", cfg, secrets)
	if trust != "" {
		f.Trust = trust
	}
	return f
}

func exec(t *testing.T, set *connectors.ToolSet, name, input string) conntest.Value {
	t.Helper()
	return conntest.Call(t, set, name, input)
}

func TestSuggestClassMapsAnnotationsFailingClosed(t *testing.T) {
	cases := []struct {
		a    *sdk.ToolAnnotations
		want string
	}{
		{&sdk.ToolAnnotations{ReadOnlyHint: true}, "read"},
		{&sdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolp(false)}, "write"},
		{&sdk.ToolAnnotations{DestructiveHint: boolp(true)}, "destructive"},
		{&sdk.ToolAnnotations{ReadOnlyHint: false}, "destructive"},
		{nil, "destructive"},
	}
	for i, c := range cases {
		if got := mcpconn.SuggestClass(c.a); got != c.want {
			t.Fatal(i, got)
		}
	}
}

func TestTestListsToolsWithClassPrefill(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register, Token: TOKEN})
	res, err := mcpconn.Runtime.Test(ctx(t, config_(t, srv.URL, ""), map[string]string{"token": TOKEN}, "").Context)
	testutil.Must(t, err)
	if !res.OK || res.Detail != "4 tools available" {
		t.Fatalf("%+v", res)
	}
	data := conntest.Of(res.Data)
	got := map[string]string{}
	for _, x := range data.List("tools") {
		v := conntest.Value{V: x}
		got[v.S("name")] = v.S("suggestedClass")
		if !v.B("enabled") || v.B("saved") {
			t.Fatal(v)
		}
	}
	if got["search"] != "read" || got["add_note"] != "write" || got["delete_all"] != "destructive" || got["mystery"] != "destructive" || len(got) != 4 {
		t.Fatal(got)
	}
}

func TestRequiresTheBearerToken(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register, Token: TOKEN})
	bad, err := mcpconn.Runtime.Test(ctx(t, config_(t, srv.URL, ""), map[string]string{"token": "wrong"}, "").Context)
	testutil.Must(t, err)
	if bad.OK {
		t.Fatal("ok with a wrong token")
	}
	auths := srv.Auths()
	if auths[len(auths)-1] != "Bearer wrong" {
		t.Fatal(auths)
	}
	missing, _ := mcpconn.Runtime.Test(ctx(t, config_(t, srv.URL, ""), nil, "").Context)
	if missing.OK || missing.Detail != "This server needs a token" {
		t.Fatalf("%+v", missing)
	}
}

func TestSendsACustomHeaderInHeaderMode(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register, Token: TOKEN})
	res, err := mcpconn.Runtime.Test(ctx(t, config_(t, srv.URL, `,"authMode":"header","headerName":"Authorization"`), map[string]string{"token": "Bearer " + TOKEN}, "").Context)
	testutil.Must(t, err)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
}

func TestOffersOnlyEnabledToolsWithTheirClassUnsetOnesAsDestructive(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register, Token: TOKEN})
	cfg := config_(t, srv.URL, `,"toolPrefs":{"search":{"enabled":true,"class":"read"},"add_note":{"enabled":true,"class":"write"},"delete_all":{"enabled":false,"class":"destructive"}}`)
	running, err := mcpconn.Runtime.Start(ctx(t, cfg, map[string]string{"token": TOKEN}, "external").Context)
	testutil.Must(t, err)
	defer running.Stop(context.Background())
	set, err := running.Tools(context.Background(), turn)
	testutil.Must(t, err)
	keys := set.Keys()
	sort.Strings(keys)
	if strings.Join(keys, ",") != "add_note,mystery,search" {
		t.Fatal(keys)
	}
	cls := func(n string) connectors.PaluiTool { pt, _ := set.Get(n); return pt }
	if cls("search").Class != "read" || cls("add_note").Class != "write" || cls("mystery").Class != "destructive" || !cls("search").Untrusted {
		t.Fatal("classes")
	}
	if cls("search").Suggested != "" || cls("mystery").Suggested != "destructive" {
		t.Fatal("suggested")
	}
	if out := exec(t, set, "search", `{"q":"kopi"}`); !strings.Contains(out.String(), "found kopi") {
		t.Fatal(out)
	}
	listed, err := running.Action(context.Background(), "listTools", jsonx.NewMap[any]())
	testutil.Must(t, err)
	rows := conntest.Of(listed)
	for _, x := range rows.List("tools") {
		v := conntest.Value{V: x}
		switch v.S("name") {
		case "delete_all":
			if v.B("enabled") || !v.B("saved") {
				t.Fatal(v)
			}
		case "mystery":
			if !v.B("enabled") || v.B("saved") {
				t.Fatal(v)
			}
		}
	}
}

func TestCallToolCallsAToolDirectlyWithItsClass(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register, Token: TOKEN})
	cfg := config_(t, srv.URL, `,"toolPrefs":{"search":{"enabled":true,"class":"read"},"delete_all":{"enabled":false,"class":"destructive"},"add_note":{"enabled":true,"class":"write"}}`)
	running, err := mcpconn.Runtime.Start(ctx(t, cfg, map[string]string{"token": TOKEN}, "").Context)
	testutil.Must(t, err)
	defer running.Stop(context.Background())
	args := func(s string) *jsonx.Object { o, _ := jsonx.ParseObject([]byte(s)); return o }
	res, err := running.CallTool(context.Background(), "search", args(`{"q":"jadwal"}`))
	testutil.Must(t, err)
	if res.Class != "read" || !strings.Contains(conntest.Of(res.Output).String(), "found jadwal") {
		t.Fatalf("%+v", res)
	}
	if r, _ := running.CallTool(context.Background(), "add_note", args(`{"text":"x"}`)); r.Class != "write" || r.Output != nil {
		t.Fatalf("%+v", r)
	}
	if r, _ := running.CallTool(context.Background(), "mystery", args(`{}`)); r.Class != "destructive" || r.Output != nil {
		t.Fatalf("%+v", r)
	}
	if _, err := running.CallTool(context.Background(), "delete_all", args(`{}`)); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatal(err)
	}
	if _, err := running.CallTool(context.Background(), "nope", args(`{}`)); err == nil || !strings.Contains(err.Error(), "No tool nope") {
		t.Fatal(err)
	}
}

// A flow source calls a tool directly. One the owner hasn't classified is
// destructive there too, whatever its annotations claim, so it never runs.
func TestCallToolTreatsAnUnclassifiedReadOnlyToolAsDestructive(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register, Token: TOKEN})
	running, err := mcpconn.Runtime.Start(ctx(t, config_(t, srv.URL, ""), map[string]string{"token": TOKEN}, "").Context)
	testutil.Must(t, err)
	defer running.Stop(context.Background())
	r, err := running.CallTool(context.Background(), "search", jsonx.NewMap[any]())
	testutil.Must(t, err)
	if r.Class != "destructive" || r.Output != nil {
		t.Fatalf("%+v", r)
	}
}

func TestMarksToolsTrustedForAnOwnServer(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register})
	running, err := mcpconn.Runtime.Start(ctx(t, config_(t, srv.URL, `,"authMode":"none"`), nil, "own").Context)
	testutil.Must(t, err)
	defer running.Stop(context.Background())
	set, err := running.Tools(context.Background(), turn)
	testutil.Must(t, err)
	if pt, _ := set.Get("search"); pt.Untrusted {
		t.Fatal("untrusted")
	}
}

func TestStartFailsClearlyWhenTheServerIsDownAndToolsRecoversAfterAnOutage(t *testing.T) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: register, Token: TOKEN})
	addr := srv.Addr()
	if _, err := mcpconn.Runtime.Start(ctx(t, config_(t, "http://127.0.0.1:1/mcp", ""), map[string]string{"token": TOKEN}, "").Context); err == nil {
		t.Fatal("started against a dead server")
	}
	f := ctx(t, config_(t, srv.URL, ""), map[string]string{"token": TOKEN}, "")
	running, err := mcpconn.Runtime.Start(f.Context)
	testutil.Must(t, err)
	defer running.Stop(context.Background())
	before, err := running.Tools(context.Background(), turn)
	testutil.Must(t, err)
	if before.Len() != 4 {
		t.Fatal(before.Len())
	}
	srv.Close()
	if r := exec(t, before, "search", `{"q":"x"}`); r.Err == nil {
		t.Fatal("call succeeded on a dead server")
	}
	after, err := running.Tools(context.Background(), turn)
	testutil.Must(t, err)
	if after.Len() != 0 || f.LastStatus() != "error" {
		t.Fatal(after.Len(), f.LastStatus())
	}
	if _, err := running.Action(context.Background(), "listTools", nil); err == nil {
		t.Fatal("listTools on a dead server")
	}
	conntest.StartMCPServer(t, conntest.MCPOptions{Register: register, Token: TOKEN, Addr: addr})
	again, err := running.Tools(context.Background(), turn)
	testutil.Must(t, err)
	if again.Len() != 4 || f.LastStatus() != "on" {
		t.Fatal(again.Len(), f.LastStatus())
	}
}

func TestFlowApprovalDefaultsToOncePerVersionAndCarriesEveryRun(t *testing.T) {
	defs := []mcpconn.ToolDef{
		{Name: "send_email", Annotations: &sdk.ToolAnnotations{DestructiveHint: boolp(false)}},
		{Name: "list", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
	}
	prefs := map[string]mcpconn.ToolPref{"send_email": {Enabled: true, Class: "write", FlowApproval: "every_run"}, "list": {Enabled: true, Class: "read"}}
	var got []string
	for _, s := range mcpconn.SelectTools(defs, prefs) {
		got = append(got, s.Def.Name+":"+s.FlowApproval)
	}
	if strings.Join(got, ",") != "send_email:every_run,list:version" {
		t.Fatal(got)
	}
	listing := conntest.Of(mcpconn.Listing(defs, prefs))
	if strings.Join(listing.Strs("", "flowApproval"), ",") != "every_run,version" {
		t.Fatal(listing)
	}
}

var jpeg = []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46}

type uploads struct {
	mu   sync.Mutex
	list []string
}

func setupFile(t *testing.T, trust string, got *uploads) (*connectors.RunningConnector, *connectors.ToolSet, string, string) {
	srv := conntest.StartMCPServer(t, conntest.MCPOptions{Register: func(s *sdk.Server) {
		conntest.AddRawTool(s, "upload_attachment", "Upload a file", `{"type":"object","properties":{"file":{"type":"string"}},"required":["file"]}`, nil, func(a map[string]any) *sdk.CallToolResult {
			file := a["file"].(string)
			if strings.HasPrefix(file, "data:") {
				got.mu.Lock()
				got.list = append(got.list, file)
				got.mu.Unlock()
			}
			return conntest.TextResult("got " + file)
		})
	}})
	f := ctx(t, config_(t, srv.URL, `,"authMode":"none"`), nil, trust)
	chat, err := chats.Ensure(f.DB, chats.EnsureInput{Channel: "whatsapp", ExternalID: "120363@g.us"}, 0)
	testutil.Must(t, err)
	other, err := chats.Ensure(f.DB, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, 0)
	testutil.Must(t, err)
	key, _ := config.MasterKey(config.Get())
	saved, err := attachments.Save(f.DB, attachments.SaveInput{ChatID: chat.ID, Title: "Struk", MimeType: "image/jpeg", Bytes: jpeg}, key, 0)
	testutil.Must(t, err)
	running, err := mcpconn.Runtime.Start(f.Context)
	testutil.Must(t, err)
	tt := turn
	tt.ChatID = chat.ID
	set, err := running.Tools(context.Background(), tt)
	testutil.Must(t, err)
	return running, set, saved.Row.ID, other.ID
}

func TestSendsTheFilesContentsToAnOwnServerInPlaceOfItsRef(t *testing.T) {
	got := &uploads{}
	running, set, id, _ := setupFile(t, "own", got)
	defer running.Stop(context.Background())
	out := exec(t, set, "upload_attachment", `{"file":"palui:attachment/`+id+`"}`)
	if out.S("content.0.text") != "got palui:attachment/"+id {
		t.Fatal(out)
	}
	if len(got.list) != 1 || got.list[0] != "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(jpeg) {
		t.Fatal(got.list)
	}
	if out := exec(t, set, "upload_attachment", `{"file":"`+id+`"}`); out.S("content.0.text") != "got "+id {
		t.Fatal(out)
	}
}

func TestRefusesARefFromAnotherChatAndNeverSendsFilesToAnExternalServer(t *testing.T) {
	got := &uploads{}
	running, _, id, otherID := setupFile(t, "own", got)
	tt := turn
	tt.ChatID = otherID
	other, err := running.Tools(context.Background(), tt)
	testutil.Must(t, err)
	r := exec(t, other, "upload_attachment", `{"file":"palui:attachment/`+id+`"}`)
	if !r.B("isError") || !strings.Contains(r.S("content.0.text"), "No saved attachment") {
		t.Fatal(r)
	}
	running.Stop(context.Background())
	ext, extSet, extID, _ := setupFile(t, "external", got)
	defer ext.Stop(context.Background())
	r = exec(t, extSet, "upload_attachment", `{"file":"palui:attachment/`+extID+`"}`)
	if !r.B("isError") || !strings.Contains(r.S("content.0.text"), "own") {
		t.Fatal(r)
	}
}

func TestToModelOutputIsTheAISDKsMCPMapping(t *testing.T) {
	out := mcpconn.ToModelOutput(conntest.Of(map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "hi"},
		map[string]any{"type": "image", "data": "AAA", "mimeType": "image/png"},
		map[string]any{"type": "resource", "uri": "x"},
	}}).V)
	if got := conntest.Of(out).String(); got != `{"type":"content","value":[{"type":"text","text":"hi"},{"type":"file","mediaType":"image/png","data":{"type":"data","data":"AAA"}},{"type":"text","text":"{\"type\":\"resource\",\"uri\":\"x\"}"}]}` {
		t.Fatal(got)
	}
	if got := conntest.Of(mcpconn.ToModelOutput("plain")).String(); got != `{"type":"text","value":"plain"}` {
		t.Fatal(got)
	}
	if got := conntest.Of(mcpconn.ToModelOutput(nil)).String(); got != `{"type":"json","value":null}` {
		t.Fatal(got)
	}
}

func TestToolSchemaFillsPropertiesAndClosesAdditionalProperties(t *testing.T) {
	got := string(mcpconn.ToolSchema(mcpconn.ToolDef{InputSchema: map[string]any{"type": "object"}}))
	if got != `{"type":"object","properties":{},"additionalProperties":false}` {
		t.Fatal(got)
	}
}

// An unclassified tool runs as destructive but carries what its annotations
// suggest, for the dashboard; a saved class drops the suggestion.
func TestSelectToolsCarriesTheSuggestionOnlyForUnclassifiedTools(t *testing.T) {
	defs := []mcpconn.ToolDef{
		{Name: "search", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
		{Name: "add_note", Annotations: &sdk.ToolAnnotations{DestructiveHint: boolp(false)}},
		{Name: "mystery"},
		{Name: "list", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
	}
	prefs := map[string]mcpconn.ToolPref{"list": {Enabled: true, Class: "write"}}
	var got []string
	for _, s := range mcpconn.SelectTools(defs, prefs) {
		got = append(got, s.Def.Name+":"+s.Class+":"+s.Suggested)
	}
	if strings.Join(got, ",") != "search:destructive:read,add_note:destructive:write,mystery:destructive:destructive,list:write:" {
		t.Fatal(got)
	}
}

// "Use suggested classes": a pref for every listed tool without one, saved
// prefs left byte for byte, an odd class read as destructive.
func TestWithSuggestedFillsOnlyToolsWithoutASavedPref(t *testing.T) {
	cfg := config_(t, "https://hub.example.com/mcp", `,"toolPrefs":{"search":{"enabled":false,"class":"destructive","flowApproval":"every_run"}}`)
	listed, err := jsonx.ParseObject([]byte(`{"tools":[` +
		`{"name":"search","suggestedClass":"read","saved":true},` +
		`{"name":"add_note","suggestedClass":"write","saved":false},` +
		`{"name":"mystery","suggestedClass":"destructive","saved":false},` +
		`{"name":"odd","suggestedClass":"admin","saved":false},` +
		`{"suggestedClass":"read"},"junk"]}`))
	testutil.Must(t, err)
	prefs, n := mcpconn.WithSuggested(cfg, mcpconn.SuggestionsFrom(listed))
	b, err := jsonx.Marshal(prefs)
	testutil.Must(t, err)
	want := `{"search":{"enabled":false,"class":"destructive","flowApproval":"every_run"},"add_note":{"enabled":true,"class":"write"},"mystery":{"enabled":true,"class":"destructive"},"odd":{"enabled":true,"class":"destructive"}}`
	if n != 3 || string(b) != want {
		t.Fatal(n, string(b))
	}
	// The config it read is left alone; the caller saves the result.
	if got := mcpconn.PrefsFrom(cfg); len(got) != 1 {
		t.Fatal(got)
	}
	// Nothing left to fill: nothing added.
	cfg.Set("toolPrefs", prefs)
	if _, n := mcpconn.WithSuggested(cfg, mcpconn.SuggestionsFrom(listed)); n != 0 {
		t.Fatal(n)
	}
}
