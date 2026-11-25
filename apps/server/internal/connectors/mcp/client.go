package mcp

// A cached MCP client over Streamable HTTP (PLAN 5.4), shared by the MCP
// connector, recall, t3code and remote-mcp (connectors/src/mcp/client.ts). One
// connection per connector instance; any transport failure drops the client so
// the next call reconnects. Built on the official Go SDK; the HTTP client is
// injectable so safenet.PublicOnlyClient can guard every request.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
)

// LegacyProtocolVersion is what a legacy endpoint (no server/discover) gets
// in initialize: the newest pre-2026 version, as the AI SDK's initialize.
const LegacyProtocolVersion = "2025-11-25"

// ToolDef is one tool a server lists (ListToolsResult["tools"][number]).
type ToolDef struct {
	Name        string
	Title       string
	Description string
	// The server's input JSON schema (keys as the SDK decoded them).
	InputSchema any
	Annotations *sdk.ToolAnnotations
}

// DisplayTitle is title ?? annotations.title.
func (d ToolDef) DisplayTitle() string {
	if d.Title != "" {
		return d.Title
	}
	if d.Annotations != nil {
		return d.Annotations.Title
	}
	return ""
}

// Endpoint is McpEndpoint.
type Endpoint struct {
	URL     string
	Headers map[string]string
	// Nil uses http.DefaultClient.
	Client     *http.Client
	ClientName string
	// Skip the 2026 server/discover handshake and use initialize. For servers
	// that answer discover with a bare -32022 (T3 Code 0.1.x speaks 2025-06-18).
	LegacyProtocol bool
}

// SuggestClass is the prefill for a tool's class from its annotations.
// destructiveHint defaults to true, so only an explicit false makes a
// non-read-only tool write. No annotations means unknown: destructive.
func SuggestClass(a *sdk.ToolAnnotations) string {
	if a != nil && a.ReadOnlyHint {
		return "read"
	}
	if a != nil && a.DestructiveHint != nil && !*a.DestructiveHint {
		return "write"
	}
	return "destructive"
}

// Hinted says whether a tool's annotations say anything about its class. A
// tool without readOnlyHint or destructiveHint gets "destructive" from
// SuggestClass by default, which is no suggestion at all: it stays
// unclassified until the owner picks a class.
func Hinted(a *sdk.ToolAnnotations) bool {
	return a != nil && (a.ReadOnlyHint || a.DestructiveHint != nil)
}

// AuthHeaders is authHeaders.
func AuthHeaders(mode, headerName, token string) map[string]string {
	if mode == "none" || token == "" {
		return map[string]string{}
	}
	if mode == "bearer" {
		return map[string]string{"Authorization": "Bearer " + token}
	}
	name := strings.TrimSpace(headerName)
	if name == "" {
		name = "Authorization"
	}
	return map[string]string{name: token}
}

// headerTransport adds the endpoint's static headers to every request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return h.base.RoundTrip(r)
}

// Connection is McpConnection.
type Connection struct {
	ep    Endpoint
	ttlMs int64

	mu      sync.Mutex
	session *sdk.ClientSession
	defs    *cachedDefs
}

type cachedDefs struct {
	at    int64
	tools []ToolDef
}

// DefaultDefsTTLMs is how long a tool list is reused.
const DefaultDefsTTLMs = 5 * 60_000

// NewConnection is new McpConnection(endpoint, {defsTtlMs}); ttlMs 0 is 5 min.
func NewConnection(ep Endpoint, ttlMs int64) *Connection {
	if ttlMs <= 0 {
		ttlMs = DefaultDefsTTLMs
	}
	return &Connection{ep: ep, ttlMs: ttlMs}
}

func (c *Connection) httpClient() *http.Client {
	base := c.ep.Client
	if base == nil {
		base = http.DefaultClient
	}
	rt := base.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	cl := *base
	cl.Transport = headerTransport{base: rt, headers: c.ep.Headers}
	return &cl
}

func (c *Connection) connect(ctx context.Context) (*sdk.ClientSession, error) {
	c.mu.Lock()
	if s := c.session; s != nil {
		c.mu.Unlock()
		return s, nil
	}
	c.mu.Unlock()
	name := c.ep.ClientName
	if name == "" {
		name = "palui"
	}
	client := sdk.NewClient(&sdk.Implementation{Name: name, Version: "1.0.0"}, nil)
	transport := &sdk.StreamableClientTransport{Endpoint: c.ep.URL, HTTPClient: c.httpClient(), DisableStandaloneSSE: true, MaxRetries: -1}
	var opts *sdk.ClientSessionOptions
	if c.ep.LegacyProtocol {
		opts = &sdk.ClientSessionOptions{ProtocolVersion: LegacyProtocolVersion}
	}
	s, err := client.Connect(ctx, transport, opts)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		_ = s.Close()
		return c.session, nil
	}
	c.session = s
	return s, nil
}

// Reset drops the cached client and its tool list; the next call reconnects.
func (c *Connection) Reset() {
	c.mu.Lock()
	old := c.session
	c.session, c.defs = nil, nil
	c.mu.Unlock()
	if old != nil {
		go func() { _ = old.Close() }()
	}
}

// Close closes the client.
func (c *Connection) Close() error {
	c.mu.Lock()
	old := c.session
	c.session, c.defs = nil, nil
	c.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// withClient runs fn with a connected client, retrying once on a fresh one.
func withClient[T any](ctx context.Context, c *Connection, fn func(*sdk.ClientSession) (T, error)) (T, error) {
	s, err := c.connect(ctx)
	if err == nil {
		var out T
		if out, err = fn(s); err == nil {
			return out, nil
		}
	}
	if ctx.Err() != nil {
		var zero T
		return zero, err
	}
	c.Reset()
	s, err = c.connect(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	return fn(s)
}

// ListTools lists every tool (up to 20 pages), cached for the TTL. fresh
// skips the cache; now 0 is the wall clock.
func (c *Connection) ListTools(ctx context.Context, fresh bool, now int64) ([]ToolDef, error) {
	if now == 0 {
		now = time.Now().UnixMilli()
	}
	c.mu.Lock()
	if d := c.defs; !fresh && d != nil && now-d.at < c.ttlMs {
		c.mu.Unlock()
		return d.tools, nil
	}
	c.mu.Unlock()
	tools, err := withClient(ctx, c, func(s *sdk.ClientSession) ([]ToolDef, error) {
		var out []ToolDef
		cursor := ""
		for page := 0; page < 20; page++ {
			var params *sdk.ListToolsParams
			if cursor != "" {
				params = &sdk.ListToolsParams{Cursor: cursor}
			}
			res, err := s.ListTools(ctx, params)
			if err != nil {
				return nil, err
			}
			for _, t := range res.Tools {
				out = append(out, ToolDef{Name: t.Name, Title: t.Title, Description: t.Description, InputSchema: t.InputSchema, Annotations: t.Annotations})
			}
			cursor = res.NextCursor
			if cursor == "" {
				break
			}
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.defs = &cachedDefs{at: now, tools: tools}
	c.mu.Unlock()
	return tools, nil
}

// CallTool calls one tool directly and returns its CallToolResult as a JSON
// object. A transport failure retries once on a fresh client, so a tool that
// must not run twice needs an idempotency key in its args.
func (c *Connection) CallTool(ctx context.Context, name string, args *jsonx.Object) (*jsonx.Object, error) {
	if args == nil {
		args = jsonx.NewMap[any]()
	}
	return withClient(ctx, c, func(s *sdk.ClientSession) (*jsonx.Object, error) {
		return callOnce(ctx, s, name, args)
	})
}

func callOnce(ctx context.Context, s *sdk.ClientSession, name string, args any) (*jsonx.Object, error) {
	raw, err := jsonx.Marshal(args)
	if err != nil {
		return nil, err
	}
	res, err := s.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: json.RawMessage(raw)})
	if err != nil {
		return nil, err
	}
	return ResultObject(res)
}

// ResultObject turns a CallToolResult into JSON the way the AI SDK's schema
// reads it: content defaults to [].
func ResultObject(res *sdk.CallToolResult) (*jsonx.Object, error) {
	b, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	o, err := jsonx.ParseObject(b)
	if err != nil {
		return nil, err
	}
	if v, _ := o.Get("content"); v == nil {
		o.Set("content", []any{})
	}
	return o, nil
}

// ToolSchema is the input schema the AI SDK sends for an MCP tool:
// {...inputSchema, properties: inputSchema.properties ?? {}, additionalProperties: false}.
func ToolSchema(def ToolDef) json.RawMessage {
	o := jsonx.NewMap[any]()
	if def.InputSchema != nil {
		if b, err := json.Marshal(def.InputSchema); err == nil {
			if parsed, err := jsonx.ParseObject(b); err == nil {
				o = parsed
			}
		}
	}
	if p, ok := o.Get("properties"); !ok || p == nil {
		o.Set("properties", jsonx.NewMap[any]())
	}
	o.Set("additionalProperties", false)
	return json.RawMessage(jsonx.Stringify(o))
}

// ToModelOutput is mcpToModelOutput.
func ToModelOutput(output any) models.ToolOutput {
	if s, ok := output.(string); ok {
		return models.OutputText(s)
	}
	v := output
	if b, err := jsonx.Marshal(output); err == nil {
		if p, err := jsonx.Parse(b); err == nil {
			v = p
		}
	}
	o, ok := v.(*jsonx.Object)
	var content []any
	if ok {
		c, _ := o.Get("content")
		content, ok = c.([]any)
	}
	if !ok {
		return models.OutputJSON(v)
	}
	parts := make([]any, 0, len(content))
	for _, raw := range content {
		p, _ := raw.(*jsonx.Object)
		typ, _ := get(p, "type").(string)
		if text, has := p.Get("text"); typ == "text" && has && p != nil {
			parts = append(parts, obj("type", "text", "text", text))
			continue
		}
		if p != nil && typ == "image" {
			data, hasData := p.Get("data")
			mime, hasMime := p.Get("mimeType")
			if hasData && hasMime {
				parts = append(parts, obj("type", "file", "mediaType", mime, "data", obj("type", "data", "data", data)))
				continue
			}
		}
		parts = append(parts, obj("type", "text", "text", jsonx.Stringify(raw)))
	}
	return models.OutputContent(parts)
}

func get(o *jsonx.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// Tools builds AI SDK tools for the given definitions (toolsFromDefinitions).
// A call that fails (transport trouble, not an isError result) resets the
// connection for the next turn.
func (c *Connection) Tools(ctx context.Context, defs []ToolDef) (map[string]connectors.Tool, error) {
	if _, err := c.connect(ctx); err != nil {
		return nil, err
	}
	out := map[string]connectors.Tool{}
	for _, def := range defs {
		name := def.Name
		out[name] = connectors.Tool{
			Description:   def.Description,
			InputSchema:   ToolSchema(def),
			ToModelOutput: ToModelOutput,
			Execute: func(ctx context.Context, input json.RawMessage, call connectors.ToolCallInfo) (any, error) {
				args, err := argsOf(input)
				if err != nil {
					return nil, err
				}
				s, err := c.connect(ctx)
				if err != nil {
					c.Reset()
					return nil, err
				}
				res, err := callOnce(ctx, s, name, args)
				if err != nil {
					c.Reset()
					return nil, err
				}
				return res, nil
			},
		}
	}
	return out, nil
}

func argsOf(input json.RawMessage) (any, error) {
	if strings.TrimSpace(string(input)) == "" {
		return jsonx.NewMap[any](), nil
	}
	v, err := jsonx.Parse(input)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return jsonx.NewMap[any](), nil
	}
	return v, nil
}

// ErrNoTool is a missing tool on the server.
var ErrNoTool = errors.New("no such tool")
