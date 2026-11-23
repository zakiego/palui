package fixtures

// The dev stand-in for every connector runtime: what apps/app/scripts/
// demo-runtime.ts did for the dashboard, now that there is no separate control
// API. `palui dev serve` gives the supervisor these instead of the real
// runtimes, so a seeded scratch database can be clicked through without real
// channel tokens and without any network: each connector shows the status the
// fixture stored, a connector that is resumed goes from connecting to on after
// a moment (except `recall`, which the fixtures leave connecting on purpose),
// and the actions the dashboard calls (listModels, listTools, listContainers)
// answer with demo data.

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/web"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Stored is a connector's status as the database held it.
type Stored struct {
	Status string
	Detail *string
}

// StandIns builds the stand-in registry. It reads the stored statuses from d
// now and again after every Apply, so a fixture switched from the dashboard
// shows its own statuses once the supervisor restarts the connectors.
func StandIns(d *db.DB, manifests []connectors.Manifest, settle time.Duration) (map[string]*connectors.Runtime, error) {
	s := &standIns{settle: settle}
	if err := s.snapshot(d); err != nil {
		return nil, err
	}
	OnApplied(func(q db.Q) {
		_ = s.snapshot(q)
	})
	out := map[string]*connectors.Runtime{}
	for _, m := range manifests {
		// The web chat needs no token or network (it publishes on the bus the
		// chat stream reads), so it stays real: a message sent from the
		// dashboard gets the engine's answer back, as on a real server.
		if m.ID == web.Manifest.ID {
			out[m.ID] = web.Runtime
			continue
		}
		out[m.ID] = &connectors.Runtime{
			Manifest: m,
			Test: func(connectors.Context) (connectors.TestResult, error) {
				return connectors.TestResult{OK: true, Detail: "Reachable (demo runtime)"}, nil
			},
			Start: s.start,
		}
	}
	return out, nil
}

type standIns struct {
	settle time.Duration
	mu     sync.Mutex
	stored map[string]Stored
}

func (s *standIns) snapshot(q db.Q) error {
	// Only enabled connectors: a paused one that is started later was resumed.
	rows, err := q.Query("SELECT id, status, status_detail FROM connectors WHERE enabled = 1")
	if err != nil {
		return err
	}
	defer rows.Close()
	stored := map[string]Stored{}
	for rows.Next() {
		var id string
		var st Stored
		if err := rows.Scan(&id, &st.Status, &st.Detail); err != nil {
			return err
		}
		stored[id] = st
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.stored = stored
	s.mu.Unlock()
	return nil
}

// take returns the stored status once: a later start is a resume.
func (s *standIns) take(id string) (Stored, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.stored[id]
	delete(s.stored, id)
	return st, ok && st.Status != "not_set_up"
}

func (s *standIns) start(c connectors.Context) (*connectors.RunningConnector, error) {
	if st, ok := s.take(c.ID); ok {
		c.SetStatus(st.Status, st.Detail)
	} else {
		c.SetStatus("connecting", nil)
		if c.ID != "recall" {
			timer := time.AfterFunc(s.settle, func() { c.SetStatus("on", nil) })
			go func() {
				<-c.Ctx.Done()
				timer.Stop()
			}()
		}
	}
	id, q, typ := c.ID, c.DB, c.Type
	return &connectors.RunningConnector{
		Stop: func(context.Context) error { return nil },
		Live: func() *jsonx.Object { return jsonx.NewMap[any]() },
		Tools: func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) {
			return demoToolSet(q, id, typ)
		},
		Action: func(_ context.Context, name string, _ *jsonx.Object) (*jsonx.Object, error) {
			switch name {
			// The settings model picker lists these.
			case "listModels":
				return obj("models", js(demoModels)), nil
			// The MCP tool table lists these.
			case "listTools":
				return demoListTools(q, id)
			// The agent page's Memory tab reads these.
			case "listContainers":
				now := time.Now().UnixMilli()
				return obj("containers", []any{
					obj("container", "default", "memories", 1284, "documents", 12, "lastActivity", now-3*60_000),
					obj("container", "agent-arisan-keluarga-bani-abdul-karim-hasan-surabaya-2026", "memories", 9_876_543, "documents", 0, "lastActivity", now-400*D),
				}), nil
			}
			return obj(), nil
		},
	}, nil
}

const demoModels = `["claude-sonnet-5-5","claude-opus-5-5","claude-haiku-5-5","claude-fable-5-2","gpt-5.2","gpt-5.2-codex","o4-mini","gemini-3-pro-preview","gemini-3-flash-preview","llama-4-maverick","deepseek-v3.2","qwen3-coder-480b-a35b-instruct-with-a-long-model-id"]`

// A fake MCP tool list for the tool table, shaped like McpToolListing.
var demoTools = []struct{ name, title, description, class string }{
	{"disk_free", "Free disk space on the host", "Show free disk space on the host.", "read"},
	{"uptime", "Host uptime and load", "Show how long the host has been up and its load average.", "read"},
	{"restart_service", "Restart a service on the home server", "Restart one of the allowlisted launchd services.", "destructive"},
	{"list_deals", "", "List deals in a stage, with no title from the server.", "write"},
	{"send_email_to_a_customer_with_a_long_tool_name", "Send an email to a customer", "Sends an email from the sales inbox. Other people read it, so flows should ask every run.", "write"},
}

// builtinTools are demo-runtime.ts's BUILTIN_TOOLS: what the agent page's
// Tools tab lists for the skills that are on.
var builtinTools = map[string][]struct {
	tool, class, title   string
	guestSafe, untrusted bool
}{
	"reminders": {
		{tool: "buat_pengingat", class: "write", title: "Bikin pengingat"},
		{tool: "list_reminders", class: "read", title: "Lihat pengingat"},
		{tool: "hapus_pengingat", class: "destructive", title: "Hapus pengingat"},
	},
	"webfetch": {{tool: "fetch_page", class: "read", title: "Ambil isi halaman web", guestSafe: true, untrusted: true}},
	"notes":    {{tool: "tambah_instruksi", class: "write", title: "Tambah instruksi tetap"}},
	"chats":    {{tool: "cari_obrolan", class: "read"}},
	"printer": {
		{tool: "printer_status", class: "read", title: "Check the printer"},
		{tool: "print_file", class: "write", title: "Print a file"},
	},
	"http": {
		{tool: "http_request", class: "read", title: "ElevenLabs: read", untrusted: true},
		{tool: "http_request_write", class: "write", title: "ElevenLabs: send", untrusted: true},
	},
}

// builtinData is each built-in skill's data scope, as the real tools declare it.
var builtinData = map[string]string{
	"reminders": connectors.DataChat,
	"webfetch":  connectors.DataNone,
	"notes":     connectors.DataAgent,
	"chats":     connectors.DataAgent,
	"printer":   connectors.DataChat,
	"http":      connectors.DataShared,
}

// demoToolSet is demo-runtime.ts's agentTools for one connector: the fake MCP
// list for an MCP connector and the built-in list for a skill, only while
// the connector is on. A call answers with a fixed text; nothing runs.
func demoToolSet(q db.Q, id, typ string) (*connectors.ToolSet, error) {
	set := connectors.NewToolSet()
	row, ok, err := connectors.Get(q, id)
	if err != nil || !ok || row.Status != "on" {
		return set, err
	}
	if row.Kind != "skill" && row.Kind != "tools" && row.Kind != "memory" {
		return set, nil
	}
	add := func(name, class, title string, guestSafe, untrusted bool) {
		desc := title
		if desc == "" {
			desc = name
		}
		set.Set(name, connectors.PaluiTool{
			Tool: connectors.Tool{
				Description: desc,
				InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
				Execute: func(context.Context, json.RawMessage, connectors.ToolCallInfo) (any, error) {
					return "demo runtime: nothing ran", nil
				},
			},
			Class: class, Title: title, GuestSafe: guestSafe, Untrusted: untrusted, Data: builtinData[typ],
		})
	}
	if typ == "mcp" {
		for _, t := range demoTools {
			add(t.name, t.class, t.title, false, false)
		}
		return set, nil
	}
	for _, t := range builtinTools[typ] {
		add(t.tool, t.class, t.title, t.guestSafe, t.untrusted)
	}
	return set, nil
}

// demoListTools: `saved` follows the connector's stored toolPrefs, so Save
// tool settings shows up.
func demoListTools(q db.Q, id string) (*jsonx.Object, error) {
	prefs := jsonx.NewMap[any]()
	row, ok, err := connectors.Get(q, id)
	if err != nil {
		return nil, err
	}
	if ok && row.Config != nil {
		if p, found := row.Config.Get("toolPrefs"); found {
			if po, isObj := p.(*jsonx.Object); isObj {
				prefs = po
			}
		}
	}
	var list []any
	for _, t := range demoTools {
		pref, _ := prefs.Get(t.name)
		po, saved := pref.(*jsonx.Object)
		field := func(key string, def any) any {
			if saved {
				if v, found := po.Get(key); found && v != nil {
					return v
				}
			}
			return def
		}
		tool := obj("name", t.name)
		if t.title != "" {
			tool.Set("title", t.title)
		}
		tool.Set("description", t.description)
		tool.Set("suggestedClass", t.class)
		tool.Set("enabled", field("enabled", true))
		tool.Set("class", field("class", t.class))
		tool.Set("saved", saved)
		tool.Set("flowApproval", field("flowApproval", "version"))
		list = append(list, tool)
	}
	return obj("tools", list), nil
}

var (
	appliedMu sync.Mutex
	applied   []func(q db.Q)
)

// OnApplied registers fn to run after every successful Apply, with the
// database the fixture was written to.
func OnApplied(fn func(q db.Q)) {
	appliedMu.Lock()
	defer appliedMu.Unlock()
	applied = append(applied, fn)
}

func notifyApplied(q db.Q) {
	appliedMu.Lock()
	list := append([]func(q db.Q){}, applied...)
	appliedMu.Unlock()
	for _, fn := range list {
		fn(q)
	}
}
