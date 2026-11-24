package rpcall_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/httpapi/apitest"
	"github.com/zakiego/palui/apps/server/internal/mcpaccess"
)

// Every server function is either a tool on /mcp or excluded on purpose.
func TestEveryServerFunctionIsAnMCPToolOrExcluded(t *testing.T) {
	tools := map[string]httpapi.MCPTool{}
	names := map[string]bool{}
	for _, tool := range httpapi.MCPTools() {
		if names[tool.Name] {
			t.Errorf("tool %s twice", tool.Name)
		}
		names[tool.Name] = true
		if _, dup := tools[tool.RPC]; dup {
			t.Errorf("%s is two tools", tool.RPC)
		}
		tools[tool.RPC] = tool
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s needs a title and a description", tool.Name)
		}
		if strings.Contains(tool.Description, "—") {
			t.Errorf("%s: no em dashes", tool.Name)
		}
		if _, ex := httpapi.MCPExcluded[tool.RPC]; ex {
			t.Errorf("%s is both a tool and excluded", tool.RPC)
		}
	}
	handlers := map[string]httpapi.Handler{}
	for _, h := range httpapi.Handlers() {
		handlers[h.Name] = h
		_, isTool := tools[h.Name]
		_, excluded := httpapi.MCPExcluded[h.Name]
		if !isTool && !excluded {
			t.Errorf("%s is neither an MCP tool nor in MCPExcluded: decide", h.Name)
		}
	}
	for rpc, tool := range tools {
		h, ok := handlers[rpc]
		if !ok {
			t.Errorf("tool %s wraps %s, which isn't registered", tool.Name, rpc)
			continue
		}
		if tool.Destructive && h.Method != http.MethodPost {
			t.Errorf("%s is destructive but reads", tool.Name)
		}
		if h.Guard == httpapi.Public {
			t.Errorf("%s wraps a public function", tool.Name)
		}
	}
	for rpc := range httpapi.MCPExcluded {
		if _, ok := handlers[rpc]; !ok {
			t.Errorf("MCPExcluded names %s, which isn't registered", rpc)
		}
	}
}

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func jsonBody(t *testing.T, r *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(apitest.Body(r)), &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return m
}

func form(e *apitest.Env, path string, v url.Values) *http.Response {
	e.T.Helper()
	return e.Do(http.MethodPost, path, v.Encode(), apitest.Header("content-type", "application/x-www-form-urlencoded"))
}

// mcpClient connects the SDK's own client to /mcp with a bearer token.
func mcpClient(t *testing.T, e *apitest.Env, token string) (*sdk.ClientSession, error) {
	t.Helper()
	srv := httptest.NewServer(e.Handler)
	t.Cleanup(srv.Close)
	hc := &http.Client{Transport: bearer{token}}
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	return c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: hc, DisableStandaloneSSE: true}, nil)
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func callText(t *testing.T, s *sdk.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestMCPWithoutATokenPointsAtTheOAuthMetadata(t *testing.T) {
	e := apitest.New(t, nil)
	session := e.SignIn()
	// A dashboard session cookie isn't a way in.
	res := e.Do(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, apitest.Cookie(session), apitest.SameOrigin())
	if res.StatusCode != 401 {
		t.Fatalf("status %d", res.StatusCode)
	}
	want := `resource_metadata="` + apitest.AppURL + `/.well-known/oauth-protected-resource/mcp"`
	if got := res.Header.Get("www-authenticate"); !strings.Contains(got, want) {
		t.Fatalf("www-authenticate %q", got)
	}
	if !strings.Contains(res.Header.Get("access-control-expose-headers"), "WWW-Authenticate") {
		t.Fatal("WWW-Authenticate isn't exposed to browsers")
	}

	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		m := jsonBody(t, e.Do(http.MethodGet, p, ""))
		if m["resource"] != apitest.AppURL+"/mcp" {
			t.Fatalf("%s: resource %v", p, m["resource"])
		}
		if as := m["authorization_servers"].([]any); len(as) != 1 || as[0] != apitest.AppURL {
			t.Fatalf("%s: authorization_servers %v", p, as)
		}
	}
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/mcp"} {
		m := jsonBody(t, e.Do(http.MethodGet, p, ""))
		if m["issuer"] != apitest.AppURL || m["token_endpoint"] != apitest.AppURL+"/api/oauth/token" || m["registration_endpoint"] != apitest.AppURL+"/api/oauth/register" {
			t.Fatalf("%s: %v", p, m)
		}
	}
}

func TestOAuthConnectionEndToEnd(t *testing.T) {
	e := apitest.New(t, nil)
	session := e.SignIn()
	const callback = "https://claude.ai/api/mcp/auth_callback"

	// Registration: an http redirect that isn't loopback is refused.
	res := e.Do(http.MethodPost, "/api/oauth/register", `{"redirect_uris":["http://evil.example/cb"]}`)
	if res.StatusCode != 400 {
		t.Fatalf("http redirect registered: %d", res.StatusCode)
	}
	res = e.Do(http.MethodPost, "/api/oauth/register", `{"client_name":"Claude","redirect_uris":["`+callback+`"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"],"scope":"claudeai"}`)
	if res.StatusCode != 201 {
		t.Fatalf("register: %d %s", res.StatusCode, apitest.Body(res))
	}
	reg := jsonBody(t, res)
	clientID := reg["client_id"].(string)
	if _, ok := reg["client_secret"]; ok {
		t.Fatal("a public client got a secret")
	}

	verifier, challenge := pkce()
	authorize := func(redirect string) *http.Response {
		q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "code_challenge": {challenge},
			"code_challenge_method": {"S256"}, "state": {"st-1"}, "scope": {"claudeai"}, "resource": {apitest.AppURL + "/mcp"}}
		return e.Do(http.MethodGet, "/api/oauth/authorize?"+q.Encode(), "")
	}
	// A redirect the client didn't register is shown, never followed.
	if res := authorize("https://claude.ai/other"); res.StatusCode != 400 || res.Header.Get("location") != "" {
		t.Fatalf("unregistered redirect: %d %q", res.StatusCode, res.Header.Get("location"))
	}
	res = authorize(callback)
	loc := res.Header.Get("location")
	if res.StatusCode != 302 || !strings.HasPrefix(loc, "/app/oauth/consent?request=") {
		t.Fatalf("authorize: %d %q", res.StatusCode, loc)
	}
	reqID := strings.TrimPrefix(loc, "/app/oauth/consent?request=")

	// The consent page needs the owner's session.
	if code, _ := e.Get("", "getOAuthRequest", `{"id":"`+reqID+`"}`); code != 401 {
		t.Fatalf("consent without a session: %d", code)
	}
	code, body := e.Get(session, "getOAuthRequest", `{"id":"`+reqID+`"}`)
	if code != 200 || !strings.Contains(body, `"clientName":"Claude"`) || !strings.Contains(body, `"redirectHost":"claude.ai"`) {
		t.Fatalf("getOAuthRequest: %d %s", code, body)
	}
	code, body = e.Post(session, "decideOAuthRequest", `{"id":"`+reqID+`","allow":true}`)
	if code != 200 {
		t.Fatalf("decide: %d %s", code, body)
	}
	var decision struct{ RedirectTo string }
	_ = json.Unmarshal([]byte(body), &decision)
	back, _ := url.Parse(decision.RedirectTo)
	if back.Host != "claude.ai" || back.Query().Get("state") != "st-1" || back.Query().Get("iss") != apitest.AppURL {
		t.Fatalf("redirect %s", decision.RedirectTo)
	}
	authCode := back.Query().Get("code")
	// Answered once.
	if code, _ := e.Post(session, "decideOAuthRequest", `{"id":"`+reqID+`","allow":true}`); code != 410 {
		t.Fatalf("second decision: %d", code)
	}

	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {authCode}, "redirect_uri": {callback}, "client_id": {clientID}, "code_verifier": {strings.Repeat("x", 50)}}
	res = form(e, "/api/oauth/token", exchange)
	if res.StatusCode != 400 || jsonBody(t, res)["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: %d", res.StatusCode)
	}
	// The failed try used the code up.
	exchange.Set("code_verifier", verifier)
	if res := form(e, "/api/oauth/token", exchange); res.StatusCode != 400 {
		t.Fatalf("a used code worked: %d", res.StatusCode)
	}

	// Again, properly.
	res = authorize(callback)
	reqID = strings.TrimPrefix(res.Header.Get("location"), "/app/oauth/consent?request=")
	_, body = e.Post(session, "decideOAuthRequest", `{"id":"`+reqID+`","allow":true}`)
	_ = json.Unmarshal([]byte(body), &decision)
	back, _ = url.Parse(decision.RedirectTo)
	exchange.Set("code", back.Query().Get("code"))
	res = form(e, "/api/oauth/token", exchange)
	if res.StatusCode != 200 {
		t.Fatalf("token: %d %s", res.StatusCode, apitest.Body(res))
	}
	tok := jsonBody(t, res)
	access, refreshTok := tok["access_token"].(string), tok["refresh_token"].(string)
	if !strings.HasPrefix(access, "pla_") || !strings.HasPrefix(refreshTok, "plr_") || tok["token_type"] != "Bearer" || tok["expires_in"].(float64) != 3600 {
		t.Fatalf("token response %v", tok)
	}

	s, err := mcpClient(t, e, access)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != len(httpapi.MCPTools()) {
		t.Fatalf("%d tools, want %d", len(list.Tools), len(httpapi.MCPTools()))
	}
	text, isErr := callText(t, s, "get_settings", nil)
	if isErr || !strings.Contains(text, `"timezone"`) {
		t.Fatalf("get_settings: %v %s", isErr, text)
	}
	text, isErr = callText(t, s, "get_agent", map[string]any{"slug": ""})
	if !isErr || !strings.Contains(text, "Invalid arguments") {
		t.Fatalf("bad arguments: %v %s", isErr, text)
	}
	_ = s.Close()

	// The connection shows in the dashboard, with when it was last used.
	code, body = e.Get(session, "getMcpAccess", "")
	if code != 200 || !strings.Contains(body, `"kind":"oauth"`) || !strings.Contains(body, `"name":"Claude"`) || !strings.Contains(body, `"url":"`+apitest.AppURL+`/mcp"`) {
		t.Fatalf("getMcpAccess: %d %s", code, body)
	}

	// Refresh rotates both tokens; the old access token stops working.
	refreshForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshTok}, "client_id": {clientID}}
	res = form(e, "/api/oauth/token", refreshForm)
	if res.StatusCode != 200 {
		t.Fatalf("refresh: %d %s", res.StatusCode, apitest.Body(res))
	}
	rotated := jsonBody(t, res)
	if rotated["refresh_token"] == refreshTok || rotated["access_token"] == access {
		t.Fatal("refresh didn't rotate")
	}
	bearerCall := func(token string) int {
		return e.Do(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
			apitest.Header("authorization", "Bearer "+token), apitest.Header("accept", "application/json, text/event-stream")).StatusCode
	}
	if c := bearerCall(access); c != 401 {
		t.Fatalf("old access token: %d", c)
	}
	if c := bearerCall(rotated["access_token"].(string)); c != 200 {
		t.Fatalf("new access token: %d", c)
	}
	// A retry with the old refresh token inside the grace window still works.
	if res := form(e, "/api/oauth/token", refreshForm); res.StatusCode != 200 {
		t.Fatalf("retry in grace: %d", res.StatusCode)
	}
	// After it, the old refresh token is a replay and cuts the connection.
	e.Now.Add(mcpaccess.RefreshGrace + 1)
	if res := form(e, "/api/oauth/token", refreshForm); res.StatusCode != 400 {
		t.Fatalf("replay: %d", res.StatusCode)
	}
	_, body = e.Get(session, "getMcpAccess", "")
	if strings.Contains(body, `"kind":"oauth"`) {
		t.Fatalf("replay didn't cut the connection: %s", body)
	}
}

func TestOAuthDenyAndExpiry(t *testing.T) {
	e := apitest.New(t, nil)
	session := e.SignIn()
	reg := jsonBody(t, e.Do(http.MethodPost, "/api/oauth/register", `{"client_name":"CLI","redirect_uris":["http://127.0.0.1:33418/callback"]}`))
	clientID := reg["client_id"].(string)
	_, challenge := pkce()
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"http://127.0.0.1:50000/callback"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"s"}}
	// Loopback redirects match on any port.
	res := e.Do(http.MethodGet, "/api/oauth/authorize?"+q.Encode(), "")
	if res.StatusCode != 302 {
		t.Fatalf("loopback port: %d %s", res.StatusCode, apitest.Body(res))
	}
	reqID := strings.TrimPrefix(res.Header.Get("location"), "/app/oauth/consent?request=")
	_, body := e.Post(session, "decideOAuthRequest", `{"id":"`+reqID+`","allow":false}`)
	if !strings.Contains(body, "error=access_denied") || !strings.Contains(body, "127.0.0.1:50000") {
		t.Fatalf("deny: %s", body)
	}

	// No PKCE: back to the client with an error.
	q.Del("code_challenge")
	res = e.Do(http.MethodGet, "/api/oauth/authorize?"+q.Encode(), "")
	if loc := res.Header.Get("location"); res.StatusCode != 302 || !strings.Contains(loc, "error=invalid_request") {
		t.Fatalf("no pkce: %d %q", res.StatusCode, loc)
	}

	// A request left on the consent page expires.
	q.Set("code_challenge", challenge)
	res = e.Do(http.MethodGet, "/api/oauth/authorize?"+q.Encode(), "")
	reqID = strings.TrimPrefix(res.Header.Get("location"), "/app/oauth/consent?request=")
	e.Now.Add(mcpaccess.RequestTTL + 1)
	if code, _ := e.Get(session, "getOAuthRequest", `{"id":"`+reqID+`"}`); code != 410 {
		t.Fatalf("expired request: %d", code)
	}
}

func TestPersonalTokens(t *testing.T) {
	e := apitest.New(t, nil)
	session := e.SignIn()
	code, body := e.Post(session, "createMcpToken", `{"name":"  mcporter on the mini  "}`)
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		Token string
		Grant struct{ ID, Name, TokenHint string }
	}
	_ = json.Unmarshal([]byte(body), &created)
	if !strings.HasPrefix(created.Token, "plt_") || created.Grant.Name != "mcporter on the mini" || !strings.HasSuffix(created.Token, created.Grant.TokenHint) {
		t.Fatalf("created %+v", created)
	}
	// The token itself is never listed again.
	_, list := e.Get(session, "getMcpAccess", "")
	if strings.Contains(list, created.Token) {
		t.Fatal("the token is listed")
	}
	// Writes need the dashboard's origin, like every write.
	res := e.Do(http.MethodPost, "/api/rpc/createMcpToken", `{"name":"x"}`, apitest.Cookie(session))
	if res.StatusCode != 403 {
		t.Fatalf("cross-origin create: %d", res.StatusCode)
	}

	s, err := mcpClient(t, e, created.Token)
	if err != nil {
		t.Fatal(err)
	}
	text, isErr := callText(t, s, "list_agents", nil)
	if isErr {
		t.Fatalf("list_agents: %s", text)
	}
	_ = s.Close()

	if code, body := e.Post(session, "revokeMcpAccess", `{"id":"`+created.Grant.ID+`"}`); code != 200 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if _, err := mcpClient(t, e, created.Token); err == nil {
		t.Fatal("a revoked token still connects")
	}
	if code, _ := e.Post(session, "revokeMcpAccess", `{"id":"`+created.Grant.ID+`"}`); code != 404 {
		t.Fatalf("revoke twice: %d", code)
	}
}

// A tool that can delete something is marked destructive, so a client that
// confirms only destructive tools asks first.
func TestMCPToolsThatDeleteAreDestructive(t *testing.T) {
	want := map[string]bool{"delete_agent": true, "remove_contact": true, "set_agent_member": true}
	for _, tool := range httpapi.MCPTools() {
		if want[tool.Name] && !tool.Destructive {
			t.Errorf("%s can delete but isn't destructive", tool.Name)
		}
		delete(want, tool.Name)
	}
	for name := range want {
		t.Errorf("%s isn't an MCP tool", name)
	}
}

// /mcp serves both protocol eras: go-sdk's client probes server/discover and
// stays on 2026-07-28, while a client pinned to 2025-11-25 (like the old TS
// SDK in Claude Code and mcporter) goes through initialize. Both get the same
// tools and can call them.
func TestMCPServesBothProtocolEras(t *testing.T) {
	e := apitest.New(t, nil)
	session := e.SignIn()
	code, body := e.Post(session, "createMcpToken", `{"name":"eras"}`)
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct{ Token string }
	_ = json.Unmarshal([]byte(body), &created)

	srv := httptest.NewServer(e.Handler)
	t.Cleanup(srv.Close)
	connect := func(opts *sdk.ClientSessionOptions) *sdk.ClientSession {
		t.Helper()
		c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
		hc := &http.Client{Transport: bearer{created.Token}}
		s, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: hc, DisableStandaloneSSE: true}, opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	modern := connect(nil)
	legacy := connect(&sdk.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if v := modern.InitializeResult().ProtocolVersion; v != "2026-07-28" {
		t.Fatalf("modern client negotiated %q", v)
	}
	if v := legacy.InitializeResult().ProtocolVersion; v != "2025-11-25" {
		t.Fatalf("legacy client negotiated %q", v)
	}

	names := func(s *sdk.ClientSession) string {
		t.Helper()
		res, err := s.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tool := range res.Tools {
			out = append(out, tool.Name)
		}
		return strings.Join(out, " ")
	}
	if m, l := names(modern), names(legacy); m != l || !strings.Contains(" "+m+" ", " list_agents ") {
		t.Fatalf("tools differ:\nmodern: %s\nlegacy: %s", m, l)
	}
	mText, mErr := callText(t, modern, "list_agents", nil)
	lText, lErr := callText(t, legacy, "list_agents", nil)
	if mErr || lErr || mText != lText {
		t.Fatalf("list_agents differs:\nmodern: %s\nlegacy: %s", mText, lText)
	}
}
