// Package remotemcp is the remote-mcp connector (connectors/src/remote-mcp):
// add, list and remove MCP server connectors from chat. A server added here is
// an ordinary mcp connector row (id mcp-<name>, external trust, no auth) with
// addedFromChat set, so its runtime only ever talks to public addresses.
package remotemcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	mcpconn "github.com/zakiego/palui/apps/server/internal/connectors/mcp"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/safenet"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

var Manifest = connectors.Manifest{
	ID:          "remote-mcp",
	Kind:        "skill",
	Name:        "Remote MCP setup",
	Icon:        "server-cog",
	Summary:     "Add and remove public MCP servers from chat.",
	Description: "Ask Palui to add a public MCP server (Streamable HTTP, no auth) and it checks the URL, connects once to list the tools, and asks you before saving it as an MCP server connector. Only https URLs on public addresses are accepted, checked again on every request. Servers that need a token are added from the Connectors page, so the token never goes through chat.",
	Builtin:     true,
	Setup:       []connectors.SetupStep{},
	PauseNote:   "Pausing hides the add, list and remove tools. Servers already added keep running.",
	Docs: &connectors.Docs{
		Commands: []string{"tambahin MCP server jadwal-sholat, url https://sholat.example.com/mcp"},
		Notes: []string{
			"Adding and removing a server is destructive: it always asks first, and scheduled jobs can't do it.",
			"A server added from chat gets the id mcp-<name>, external trust, and its tools prefilled from their read-only and destructive hints. Change them on the Connectors page.",
			"From chat Palui can only remove servers it added from chat. Others are removed on the Connectors page.",
		},
	},
	ParseConfig: kit.ParseConfig(zod.Object()),
}

// ServerName is SERVER_NAME: server names become the connector id suffix.
var ServerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

const serverNameSource = `^[a-z0-9][a-z0-9-]{0,39}$`

// ProbeTimeout is PROBE_TIMEOUT_MS.
var ProbeTimeout = 15 * time.Second

// Deps are RemoteMcpDeps.
type Deps struct {
	DB        *db.DB
	MasterKey string
	// Pokes the supervisor so the new server starts now.
	Reconcile func()
	Resolver  safenet.Resolver
	// The HTTP client the probe goes through (wrapped by the public guard).
	Client *http.Client
	Clock  func() int64
	// The agent whose chat this is ("" is the default agent): it lists and
	// removes only the servers it can call.
	AgentID string
	// Servers added here belong to AgentID instead of every agent. True for
	// every agent but the default, so a friends' group agent can't change
	// what the owner's agent calls.
	Own bool
}

func shownURL(raw string) any {
	if raw == "" {
		return nil
	}
	u, err := safenet.ParseURL(raw)
	if err != nil {
		return nil
	}
	return u.Origin() + u.Path
}

func serverView(row db.Connector) any {
	c := mcpconn.ConfigFrom(row.Config)
	enabled := 0
	for _, p := range mcpconn.PrefsFrom(row.Config) {
		if p.Enabled {
			enabled++
		}
	}
	auth := c.AuthMode
	if auth == "" {
		auth = "bearer"
	}
	return kit.O("id", row.ID, "name", row.Label, "url", shownURL(c.URL), "status", row.Status, "auth", auth, "toolsEnabled", enabled, "addedFromChat", c.AddedFromChat)
}

func idFor(nameOrID string) string {
	v := strings.ToLower(strx.Trim(nameOrID))
	if strings.HasPrefix(v, "mcp-") {
		return v
	}
	return "mcp-" + v
}

// CheckServerURL is checkServerUrl: https and public, before anything connects.
func CheckServerURL(ctx context.Context, raw string, resolver safenet.Resolver) (*safenet.URL, error) {
	u, err := safenet.AssertPublicURL(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" {
		return nil, &safenet.Error{Code: "blocked_protocol", Message: "Only https URLs can be added from chat"}
	}
	if _, err := safenet.ResolvePublic(ctx, u.HostOf(), resolver); err != nil {
		return nil, err
	}
	return u, nil
}

var (
	addInput = zod.Object(
		zod.F("name", zod.String().Regex(ServerName, serverNameSource, "lowercase letters, digits and dashes").Describe("Short name, e.g. jadwal-sholat. The connector id becomes mcp-<name>.")),
		zod.F("url", zod.String().Max(500).Describe("The server's Streamable HTTP endpoint, https only, e.g. https://example.com/mcp.")),
	)
	removeInput = zod.Object(zod.F("name", zod.String().Min(1).Max(60).Describe("Server name or connector id, e.g. jadwal-sholat or mcp-jadwal-sholat.")))
)

// Tools is remoteMcpTools(deps).
func Tools(deps Deps) *connectors.ToolSet {
	d := deps.DB
	clock := deps.Clock
	if clock == nil {
		clock = func() int64 { return time.Now().UnixMilli() }
	}
	data := connectors.DataShared
	var owner []string
	if deps.Own {
		data = connectors.DataAgent
		owner = []string{deps.AgentID}
	}
	set := connectors.NewToolSet()
	set.Set("add_mcp_server", connectors.PaluiTool{
		Data:  data,
		Class: "destructive",
		Title: "Add an MCP server",
		Tool: kit.Tool("Add a public remote MCP server (Streamable HTTP, no auth) as a connector, only when the owner asks for it with the URL. Checks the URL, connects once to list its tools, then saves it. The tools are usable from the owner's next message. Servers that need a token: tell the owner to add them on the Connectors page instead.", addInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				name := in.S("name")
				id := "mcp-" + name
				if _, found, err := connectors.Get(d, id); err != nil {
					return nil, err
				} else if found {
					return kit.Fail(fmt.Sprintf("Udah ada konektor %s. Pakai nama lain, atau hapus yang lama dulu.", id)), nil
				}
				u, err := CheckServerURL(ctx, in.S("url"), deps.Resolver)
				if err != nil {
					var se *safenet.Error
					if errors.As(err, &se) {
						return kit.Fail("URL ditolak: " + se.Message), nil
					}
					return nil, err
				}
				var base http.RoundTripper
				if deps.Client != nil {
					base = deps.Client.Transport
				}
				conn := mcpconn.NewConnection(mcpconn.Endpoint{URL: u.Href(), Headers: map[string]string{}, Client: safenet.PublicOnlyClient(base, deps.Resolver)}, 0)
				pctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
				defs, err := conn.ListTools(pctx, true, 0)
				timedOut := pctx.Err() == context.DeadlineExceeded
				cancel()
				conn.Close()
				if err != nil {
					msg := err.Error()
					if timedOut {
						msg = "no answer within 15 s"
					}
					return kit.Fail(fmt.Sprintf("Nggak bisa konek ke server MCP itu: %s. Kalau servernya butuh token, tambahin dari halaman Connectors.", msg)), nil
				}
				prefs := jsonx.NewMap[any]()
				listed := make([]any, len(defs))
				for i, def := range defs {
					cls := mcpconn.SuggestClass(def.Annotations)
					prefs.Set(def.Name, kit.O("enabled", true, "class", cls))
					listed[i] = kit.O("name", id+"__"+def.Name, "class", cls, "description", strx.Slice16(def.Description, 200))
				}
				now := clock()
				label, trust, on := name, "external", true
				if _, err := connectors.Upsert(d, connectors.UpsertInput{
					ID: id, Manifest: mcpconn.Manifest.Row(), Label: &label, Trust: &trust, Enabled: &on,
					Config:   kit.O("url", u.Href(), "authMode", "none", "toolPrefs", prefs, "addedFromChat", true),
					AgentIDs: owner, AgentIDsSet: owner != nil,
				}, deps.MasterKey, now); err != nil {
					return nil, err
				}
				if err := connectors.Connect(d, id, now); err != nil {
					return nil, err
				}
				if _, err := events.Log(d, events.Input{ConnectorID: &id, Kind: "mcp.added", Message: fmt.Sprintf("MCP server %s added from chat", name), Data: kit.O("url", shownURL(u.Href()), "tools", len(defs))}, now); err != nil {
					return nil, err
				}
				if deps.Reconcile != nil {
					deps.Reconcile()
				}
				return kit.O("ok", true, "id", id, "message", fmt.Sprintf("Server %s ditambahin dengan %d tool. Bisa dipakai mulai pesan berikutnya. Atur tool-nya di halaman Connectors.", name, len(defs)), "tools", listed), nil
			}),
	})
	set.Set("list_mcp_servers", connectors.PaluiTool{
		Data:  data,
		Class: "read",
		Title: "List MCP servers",
		Tool: kit.Tool("List the MCP server connectors with their status and how many tools are on. Never shows tokens.", zod.Object(),
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				rows, err := connectors.List(d)
				if err != nil {
					return nil, err
				}
				var list []any
				for _, r := range rows {
					if r.Type == "mcp" && connectors.ServesAgent(r, deps.AgentID) {
						list = append(list, serverView(r))
					}
				}
				if list == nil {
					list = []any{}
				}
				return kit.O("ok", true, "count", len(list), "servers", list, "message", kit.If(len(list) == 0, "Belum ada server MCP.")), nil
			}),
	})
	set.Set("remove_mcp_server", connectors.PaluiTool{
		Data:  data,
		Class: "destructive",
		Title: "Remove an MCP server",
		Tool: kit.Tool("Remove an MCP server that was added from chat, by name or id (from list_mcp_servers). Servers set up on the Connectors page are removed there.", removeInput,
			func(ctx context.Context, in kit.Input, call connectors.ToolCallInfo) (any, error) {
				id := idFor(in.S("name"))
				row, found, err := connectors.Get(d, id)
				if err != nil {
					return nil, err
				}
				if !found || row.Type != "mcp" || !connectors.ServesAgent(row, deps.AgentID) {
					return kit.Fail(fmt.Sprintf("Nggak ada server MCP %s. Panggil list_mcp_servers untuk lihat id-nya.", id)), nil
				}
				if deps.Own && !connectors.OnlyAgent(row, deps.AgentID) {
					return kit.Fail(fmt.Sprintf("%s dipakai agent lain juga. Hapus dari halaman Connectors ya.", row.Label)), nil
				}
				if !mcpconn.ConfigFrom(row.Config).AddedFromChat {
					return kit.Fail(fmt.Sprintf("%s dipasang dari dashboard. Hapus dari halaman Connectors ya.", row.Label)), nil
				}
				if err := connectors.Delete(d, id, clock()); err != nil {
					return nil, err
				}
				if deps.Reconcile != nil {
					deps.Reconcile()
				}
				return kit.O("ok", true, "message", fmt.Sprintf("Server %s dihapus.", row.Label)), nil
			}),
	})
	return set
}

var Runtime = &connectors.Runtime{
	Manifest: Manifest,
	Test: func(c connectors.Context) (connectors.TestResult, error) {
		rows, err := connectors.List(c.DB)
		if err != nil {
			return connectors.TestResult{}, err
		}
		n := 0
		for _, r := range rows {
			if r.Type == "mcp" {
				n++
			}
		}
		if n > 0 {
			return connectors.TestResult{OK: true, Detail: fmt.Sprintf("%d MCP server%s", n, kit.Plural(n))}, nil
		}
		return connectors.TestResult{OK: true, Detail: "Ready, no MCP servers yet"}, nil
	},
	Start: func(c connectors.Context) (*connectors.RunningConnector, error) {
		key, _ := config.MasterKey(config.Get())
		deps := Deps{DB: c.DB, MasterKey: key, Reconcile: func() {
			if c.Services != nil && c.Services.Reconcile != nil {
				c.Services.Reconcile()
			}
		}}
		return &connectors.RunningConnector{
			Stop: func(context.Context) error { return nil },
			Tools: func(_ context.Context, turn connectors.TurnInfo) (*connectors.ToolSet, error) {
				d := deps
				d.AgentID = turn.AgentID
				if turn.AgentID != "" {
					a, ok, err := agents.Get(c.DB, turn.AgentID)
					if err != nil {
						return nil, err
					}
					// An agent that's gone keeps to itself (fail closed).
					d.Own = !ok || !a.IsDefault
				}
				return Tools(d), nil
			},
		}, nil
	},
}
