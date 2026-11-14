package conntest

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Status is one SetStatus call.
type Status struct {
	Status string
	Detail *string
}

// Event is one Event call.
type Event struct {
	Kind    string
	Message string
}

// FakeCtx is mcp/test-helpers.ts fakeCtx: a connector context on a fresh
// memory database that records statuses, config and secret patches and events.
type FakeCtx struct {
	connectors.Context
	mu            sync.Mutex
	Statuses      []Status
	Patches       []*jsonx.Object
	SecretPatches []*jsonx.Map[string]
	Events        []Event
}

// NewCtx builds a FakeCtx. d nil is a new memory database; config and
// secrets nil are empty. Trust is "own" unless set after.
func NewCtx(t testing.TB, d *db.DB, id string, config *jsonx.Object, secrets map[string]string) *FakeCtx {
	if d == nil {
		d = testutil.DB(t)
	}
	if config == nil {
		config = jsonx.NewMap[any]()
	}
	sec := jsonx.NewMap[string]()
	for k, v := range secrets {
		sec.Set(k, v)
	}
	f := &FakeCtx{}
	f.Context = connectors.Context{
		ID: id, Type: id, Config: config, Secrets: sec, Trust: "own", DB: d, Log: logx.Silent, DataDir: t.TempDir(),
		Ctx:      context.Background(),
		Services: &connectors.Services{},
		SetStatus: func(status string, detail *string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.Statuses = append(f.Statuses, Status{status, detail})
		},
		Event: func(kind, message, level string, data *jsonx.Object) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.Events = append(f.Events, Event{kind, message})
		},
		PatchConfig: func(p *jsonx.Object) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.Patches = append(f.Patches, p)
		},
		PatchSecrets: func(p *jsonx.Map[string]) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.SecretPatches = append(f.SecretPatches, p)
		},
	}
	return f
}

// Ctx is NewCtx's connector context.
func Ctx(t testing.TB, d *db.DB, id string, config *jsonx.Object, secrets map[string]string) connectors.Context {
	return NewCtx(t, d, id, config, secrets).Context
}

// LastStatus is the newest SetStatus call ("" when none).
func (f *FakeCtx) LastStatus() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Statuses) == 0 {
		return ""
	}
	return f.Statuses[len(f.Statuses)-1].Status
}

// PatchList copies the config patches.
func (f *FakeCtx) PatchList() []*jsonx.Object {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*jsonx.Object(nil), f.Patches...)
}

// MCPServer is test-helpers.ts startMcpServer: an MCP server (Streamable
// HTTP, stateless, JSON responses) on 127.0.0.1 serving Path, with an
// optional bearer token and extra REST routes next to it.
type MCPServer struct {
	URL   string
	mu    sync.Mutex
	auths []string
	srv   *http.Server
	ln    net.Listener
}

// MCPOptions configure StartMCPServer.
type MCPOptions struct {
	Register func(s *sdk.Server)
	// Requests to Path without "Authorization: Bearer <Token>" get a 401.
	Token string
	// Default /mcp.
	Path string
	// "127.0.0.1:<port>" to restart a server on the same URL; "" is ephemeral.
	Addr string
	// Extra routes; return true when handled.
	Rest func(w http.ResponseWriter, r *http.Request, body string) bool
}

// Auths is every Authorization header the MCP path saw, in order.
func (m *MCPServer) Auths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.auths...)
}

// Close stops the server and drops its connections.
func (m *MCPServer) Close() {
	_ = m.srv.Close()
}

// Addr is the listener's address.
func (m *MCPServer) Addr() string { return m.ln.Addr().String() }

// StartMCPServer starts the server; it is closed when the test ends.
func StartMCPServer(t testing.TB, o MCPOptions) *MCPServer {
	path := o.Path
	if path == "" {
		path = "/mcp"
	}
	addr := o.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	m := &MCPServer{ln: ln}
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
		s := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
		if o.Register != nil {
			o.Register(s)
		}
		return s
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			raw, _ := io.ReadAll(r.Body)
			if o.Rest != nil && o.Rest(w, r, string(raw)) {
				return
			}
			w.WriteHeader(404)
			return
		}
		m.mu.Lock()
		m.auths = append(m.auths, r.Header.Get("Authorization"))
		m.mu.Unlock()
		if o.Token != "" && r.Header.Get("Authorization") != "Bearer "+o.Token {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32001, "message": "missing or invalid API key"}, "id": nil})
			return
		}
		handler.ServeHTTP(w, r)
	})
	m.srv = &http.Server{Handler: mux}
	go func() { _ = m.srv.Serve(ln) }()
	m.URL = "http://" + ln.Addr().String() + path
	t.Cleanup(m.Close)
	return m
}

// TextResult is an MCP result with one text content.
func TextResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// JSONResult is json(v): an MCP result whose text is JSON.
func JSONResult(v any) *sdk.CallToolResult {
	b, _ := json.Marshal(v)
	return TextResult(string(b))
}

// ArgsOf decodes a raw tool call's arguments.
func ArgsOf(req *sdk.CallToolRequest) map[string]any {
	out := map[string]any{}
	if req == nil || req.Params == nil || len(req.Params.Arguments) == 0 {
		return out
	}
	_ = json.Unmarshal(req.Params.Arguments, &out)
	return out
}

// AddRawTool registers a tool with a raw JSON input schema and a handler.
func AddRawTool(s *sdk.Server, name, description, schema string, ann *sdk.ToolAnnotations, h func(args map[string]any) *sdk.CallToolResult) {
	if strings.TrimSpace(schema) == "" {
		schema = `{"type":"object","properties":{}}`
	}
	s.AddTool(&sdk.Tool{Name: name, Description: description, InputSchema: json.RawMessage(schema), Annotations: ann}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return h(ArgsOf(req)), nil
	})
}
