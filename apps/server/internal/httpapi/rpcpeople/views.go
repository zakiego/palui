package rpcpeople

import (
	"math"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/notes"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// apps/app/src/server/agents.ts: the read-side shapes of the Agents pages.

type agentViewT struct {
	ID                   string             `json:"id"`
	Slug                 string             `json:"slug"`
	Name                 string             `json:"name"`
	Persona              string             `json:"persona"`
	IsDefault            bool               `json:"isDefault"`
	OwnerPowers          string             `json:"ownerPowers"`
	GuestTools           string             `json:"guestTools"`
	DailyTokenCap        int64              `json:"dailyTokenCap"`
	ToolPermissions      db.ToolPermissions `json:"toolPermissions"`
	RecallContainer      *string            `json:"recallContainer"`
	RecallReadContainers []string           `json:"recallReadContainers"`
	RecallPerChat        bool               `json:"recallPerChat"`
	Container            *string            `json:"container"`
	TelegramConnectorID  *string            `json:"telegramConnectorId"`
	AvatarImage          *string            `json:"avatarImage"`
	AvatarIcon           *string            `json:"avatarIcon"`
	AvatarColor          *string            `json:"avatarColor"`
	Chats                int                `json:"chats"`
	Instructions         int                `json:"instructions"`
	TokensToday          int64              `json:"tokensToday"`
}

// permissionsOf is readPermissions(row.toolPermissions).
func permissionsOf(a db.Agent) db.ToolPermissions {
	if !a.ToolPermissions.Valid {
		return tools.NoPermissions()
	}
	return tools.ReadPermissions([]byte(jsonx.Stringify(a.ToolPermissions.V)))
}

func agentView(q db.Q, a db.Agent, chatCount int, now int64) (agentViewT, error) {
	ins, err := notes.ListInstructions(q, notes.ListOptions{AgentID: a.ID}, now)
	if err != nil {
		return agentViewT{}, err
	}
	tokens, err := budget.AgentTokensToday(q, a.ID, now)
	if err != nil {
		return agentViewT{}, err
	}
	return agentViewT{
		ID: a.ID, Slug: a.Slug, Name: a.Name, Persona: a.Persona, IsDefault: a.IsDefault, OwnerPowers: a.OwnerPowers, GuestTools: a.GuestTools,
		DailyTokenCap: a.DailyTokenCap, ToolPermissions: permissionsOf(a), RecallContainer: a.RecallContainer, RecallReadContainers: agents.ReadContainers(a),
		RecallPerChat: a.RecallPerChat, Container: agents.Container(a, ""), TelegramConnectorID: a.TelegramConnectorID,
		AvatarImage: a.AvatarImage, AvatarIcon: a.AvatarIcon, AvatarColor: a.AvatarColor,
		Chats: chatCount, Instructions: len(ins), TokensToday: tokens,
	}, nil
}

type telegramBot struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Username *string `json:"username"`
	Status   string  `json:"status"`
}

func configString(c db.Connector, key string) (string, bool) {
	if c.Config == nil {
		return "", false
	}
	v, _ := c.Config.Get(key)
	s, ok := v.(string)
	return s, ok
}

// telegramBots are the agent bots (tgbot connectors).
func telegramBots(q db.Q) ([]telegramBot, error) {
	list, err := connectors.List(q)
	if err != nil {
		return nil, err
	}
	out := []telegramBot{}
	for _, c := range list {
		if c.Type != "tgbot" {
			continue
		}
		b := telegramBot{ID: c.ID, Label: c.Label, Status: c.Status}
		if s, ok := configString(c, "botUsername"); ok {
			b.Username = &s
		}
		out = append(out, b)
	}
	return out, nil
}

const day = 24 * 60 * 60 * 1000

type agentToolsT struct {
	Tools  []tools.CatalogTool `json:"tools"`
	Error  *string             `json:"error"`
	Used   *jsonx.Map[int64]   `json:"used"`
	Scopes *jsonx.Map[*string] `json:"scopes"`
}

// agentTools is the Tools tab: what the runtime would offer the agent now
// (nil when it can't say) and its successful calls per tool over 7 days.
func agentTools(c *httpapi.Call, a db.Agent, now int64) (agentToolsT, error) {
	out := agentToolsT{Used: jsonx.NewMap[int64](), Scopes: jsonx.NewMap[*string]()}
	if rt, err := runtimeOf(c); err != nil {
		msg := runtimeErrorMessage(err)
		out.Error = &msg
	} else if list, err := rt.AgentTools(c.Ctx, a.ID); err != nil {
		msg := runtimeErrorMessage(err)
		out.Error = &msg
	} else {
		if list == nil {
			list = []tools.CatalogTool{}
		}
		out.Tools = list
	}
	q := c.DB()
	rows, err := q.Query(`select "tool_calls"."tool", count(*) from "tool_calls" inner join "runs" on "runs"."id" = "tool_calls"."run_id" where ("runs"."agent_id" = ? and "tool_calls"."status" = ? and "tool_calls"."created_at" >= ?) group by "tool_calls"."tool"`, a.ID, "ok", now-7*day)
	type used struct {
		tool string
		n    int64
	}
	all, err := db.All(rows, err, func(s db.Scanner) (used, error) {
		var u used
		return u, s.Scan(&u.tool, &u.n)
	})
	if err != nil {
		return agentToolsT{}, err
	}
	for _, u := range all {
		out.Used.Set(u.tool, u.n)
	}
	list, err := connectors.List(q)
	if err != nil {
		return agentToolsT{}, err
	}
	for _, conn := range list {
		if conn.Kind == "tools" {
			out.Scopes.Set(conn.ID, conn.AgentID)
		}
	}
	return out, nil
}

type memoryConnector struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`
}

type agentMemoryT struct {
	Container      string           `json:"container"`
	PerChat        bool             `json:"perChat"`
	Reads          []string         `json:"reads"`
	ChatContainers int              `json:"chatContainers"`
	Connector      *memoryConnector `json:"connector"`
	Stats          any              `json:"stats"`
	Error          *string          `json:"error"`
}

// num is a JS number read off the runtime's answer: NaN when it isn't one.
func num(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return math.NaN()
}

// numOrZero is `v ?? 0` for a value that should be a number.
func numOrZero(v any) float64 {
	if v == nil {
		return 0
	}
	return num(v)
}

// jsNum writes a float64 the way JSON.stringify does: NaN is null.
func jsNum(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

// agentMemory is the Memory tab: where the agent's memory lives and how much
// is in it. With one container per chat, the numbers add up every chat's.
func agentMemory(c *httpapi.Call, a db.Agent) (agentMemoryT, error) {
	list, err := connectors.List(c.DB())
	if err != nil {
		return agentMemoryT{}, err
	}
	var conn *db.Connector
	for i := range list {
		if list[i].Kind == "memory" {
			conn = &list[i]
			break
		}
	}
	configured := "default"
	if conn != nil {
		if s, ok := configString(*conn, "container"); ok && s != "" {
			configured = s
		}
	}
	container := configured
	if own := agents.Container(a, ""); own != nil {
		container = *own
	}
	out := agentMemoryT{Container: container, PerChat: a.RecallPerChat, Reads: agents.ReadContainers(a)}
	if conn == nil {
		return out, nil
	}
	out.Connector = &memoryConnector{ID: conn.ID, Label: conn.Label, Status: conn.Status, Enabled: conn.Enabled}
	if !conn.Enabled {
		return out, nil
	}
	fail := func(err error) (agentMemoryT, error) {
		msg := runtimeErrorMessage(err)
		out.Error = &msg
		out.Stats, out.ChatContainers = nil, 0
		return out, nil
	}
	rt, err := runtimeOf(c)
	if err != nil {
		return fail(err)
	}
	r, err := rt.Action(c.Ctx, conn.ID, "listContainers", nil)
	if err != nil {
		return fail(err)
	}
	var all []any
	if r != nil {
		if v, _ := r.Get("containers"); v != nil {
			all, _ = v.([]any)
		}
	}
	containerOf := func(x any) (string, bool) {
		o, ok := x.(*jsonx.Object)
		if !ok {
			return "", false
		}
		v, _ := o.Get("container")
		s, ok := v.(string)
		return s, ok
	}
	if a.RecallPerChat {
		prefix := agents.ChatContainerPrefix(container)
		memories, documents, last := 0.0, 0.0, math.NaN()
		lastNull := true
		n := 0
		for _, x := range all {
			name, ok := containerOf(x)
			if !ok || !strings.HasPrefix(name, prefix) {
				continue
			}
			n++
			o := x.(*jsonx.Object)
			m, _ := o.Get("memories")
			d, _ := o.Get("documents")
			l, _ := o.Get("lastActivity")
			memories += num(m)
			documents += num(d)
			prev := 0.0
			if !lastNull {
				prev = last
			}
			// Math.max(sum.lastActivity ?? 0, c.lastActivity ?? 0) || null
			mx := math.Max(prev, numOrZero(l))
			if math.IsNaN(mx) || mx == 0 {
				lastNull, last = true, 0
			} else {
				lastNull, last = false, mx
			}
		}
		stats := jsonx.NewMap[any]()
		stats.Set("container", container)
		stats.Set("memories", jsNum(memories))
		stats.Set("documents", jsNum(documents))
		if lastNull {
			stats.Set("lastActivity", nil)
		} else {
			stats.Set("lastActivity", jsNum(last))
		}
		out.ChatContainers = n
		out.Stats = stats
		return out, nil
	}
	for _, x := range all {
		if name, ok := containerOf(x); ok && name == container {
			out.Stats = x
			return out, nil
		}
	}
	stats := jsonx.NewMap[any]()
	stats.Set("container", container)
	stats.Set("memories", 0)
	stats.Set("documents", 0)
	stats.Set("lastActivity", nil)
	out.Stats = stats
	return out, nil
}
