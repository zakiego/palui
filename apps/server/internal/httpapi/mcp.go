package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/connectors/manage"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/mcpaccess"
	"github.com/zakiego/palui/apps/server/internal/version"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

// /mcp is Palui's own MCP server, for the owner's AI agents: Streamable HTTP,
// stateless, JSON responses. Every tool is one of the dashboard's server
// functions (mcp_tools.go), called as the owner, so the validation, the
// supervisor pokes and the events are the dashboard's. A bearer token is the
// only way in: a personal token (plt_) or an OAuth access token (pla_).
// Cookies are ignored, so a browser session can't be ridden cross-site.

const mcpInstructions = `Palui Agent's admin tools: the same things the owner can do in the dashboard.
Read tools (list_*, get_*) are safe. Everything else changes the live assistant at once, so confirm with the user before a write unless they asked for it.
Ids come from the list tools: list_agents for agent ids and slugs, list_chats for chat ids, list_connectors for connector ids.`

type mcpCallerKey struct{}

func (s *Server) mcpRoutes() {
	h := s.mcpHandler()
	s.mux.Handle(mcpaccess.MCPPath, h)
	s.mux.Handle(mcpaccess.MCPPath+"/", h)
}

func (s *Server) mcpHandler() http.Handler {
	var once sync.Once
	var stream http.Handler
	build := func() {
		srv := sdk.NewServer(&sdk.Implementation{Name: "palui", Title: "Palui Agent", Version: version.Version}, &sdk.ServerOptions{
			Instructions: mcpInstructions,
		})
		for _, t := range MCPTools() {
			s.addMCPTool(srv, t)
		}
		stream = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, &sdk.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,
			// The bearer token is the protection; behind the tunnel every
			// request arrives from a local address with the public Host.
			DisableLocalhostProtection: true,
		})
	}
	meta := s.o.Config.AppURL + mcpaccess.ResourceMetaPath + mcpaccess.MCPPath
	verify := func(ctx context.Context, token string, r *http.Request) (*sdkauth.TokenInfo, error) {
		c, err := mcpaccess.Verify(s.o.DB, token, s.now())
		if err != nil {
			s.o.Log.Error("mcp token check failed", logx.Fields{"error": err})
			return nil, err
		}
		if c == nil {
			return nil, sdkauth.ErrInvalidToken
		}
		if err := mcpaccess.Touch(s.o.DB, c.Grant, s.now()); err != nil {
			s.o.Log.Warn("mcp last-used write failed", logx.Fields{"error": err})
		}
		// Verify checked the expiry on the server clock; the SDK would check
		// Expiration again on the wall clock, so it's left unset.
		return &sdkauth.TokenInfo{UserID: c.User.ID, Extra: map[string]any{"caller": c}}, nil
	}
	guard := sdkauth.RequireBearerToken(verify, &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL:    meta,
		AllowMissingExpiration: true,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		once.Do(build)
		guard(stream).ServeHTTP(w, r)
	})
}

// mcpCaller is the token's owner, from the request the SDK hands a tool.
func mcpCaller(req *sdk.CallToolRequest) *mcpaccess.Caller {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil
	}
	c, _ := req.Extra.TokenInfo.Extra["caller"].(*mcpaccess.Caller)
	return c
}

func (s *Server) addMCPTool(srv *sdk.Server, t MCPTool) {
	h := lookup(t.RPC)
	if h == nil {
		// Dev-only functions aren't linked into every build.
		return
	}
	schema := []byte(`{"type":"object","properties":{},"additionalProperties":false}`)
	if h.Input != nil {
		schema = zod.JSONSchemaBytes(h.Input)
	}
	read := h.Method == http.MethodGet
	destructive := t.Destructive
	srv.AddTool(&sdk.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: json.RawMessage(schema),
		Annotations: &sdk.ToolAnnotations{
			Title:           t.Title,
			ReadOnlyHint:    read,
			DestructiveHint: &destructive,
			IdempotentHint:  read || t.Idempotent,
		},
	}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return s.runMCPTool(ctx, h, req)
	})
}

func (s *Server) runMCPTool(ctx context.Context, h *Handler, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
	caller := mcpCaller(req)
	if caller == nil {
		return nil, errors.New("no caller on the request")
	}
	var input any = zod.Undefined
	if h.Input != nil {
		input = jsonx.NewMap[any]()
		if raw := strings.TrimSpace(string(req.Params.Arguments)); raw != "" && raw != "null" {
			v, err := jsonx.Parse([]byte(raw))
			if err != nil {
				return toolError("The arguments aren't valid JSON."), nil
			}
			input = v
		}
		out, err := zod.Parse(h.Input, input)
		if err != nil {
			var ze *zod.Error
			if errors.As(err, &ze) {
				return toolError("Invalid arguments: " + zod.IssuesJSON(ze.Issues)), nil
			}
			return nil, err
		}
		input = out
	}
	res, err := h.Fn(&Call{Ctx: ctx, S: s, Viewer: caller.Viewer(), Input: input})
	if err != nil {
		msg, internal := describeError(err)
		if internal {
			s.o.Log.Error("mcp tool failed", logx.Fields{"tool": req.Params.Name, "error": err})
		}
		return toolError(msg), nil
	}
	body, err := jsonx.Marshal(res)
	if err != nil {
		return nil, err
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(body)}}}, nil
}

func toolError(msg string) *sdk.CallToolResult {
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: msg}}}
}

// describeError is the message an RPC error shows, as rpcError would send it.
// internal is true for an unexpected error (logged, like rpcError's 500).
func describeError(err error) (msg string, internal bool) {
	var he *Error
	var me *manage.Error
	var ae *auth.Error
	switch {
	case errors.As(err, &he):
		return he.Message, he.Status >= 500
	case errors.As(err, &me):
		return me.FieldsText(), false
	case errors.As(err, &ae):
		return ae.Message, false
	}
	return fmt.Sprintf("Palui couldn't do that: %v", err), true
}
