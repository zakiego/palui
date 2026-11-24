package tools

// Tools for a turn (PLAN 8.5, 5.2, 10), from tools/collect.ts: every running
// connector's tools, namespaced <connectorId>__<tool>. When there are more
// than budgets.maxTools, only a core set is active up front (tools loaded
// earlier in the session, then skills, memory and the rest) and the others are
// loadable through palui__search_tools, so nothing is dropped.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

const (
	ToolSep = "__"
	// The tool search meta tool, namespaced under the `palui` prefix.
	SearchTool = "palui" + ToolSep + "search_tools"
	// Who can read the chat a turn runs in (PLAN 19.4). "" counts as owner.
	AudienceOwner   = "owner"
	AudienceMembers = "members"
	AudienceOutside = "outside"
	// Matches loaded per search call.
	SearchLimit = 5
	// Loaded tools a session keeps, most recent first.
	StickyMax = 20
)

// ToolMeta is a collected tool's metadata.
type ToolMeta struct {
	Name        string `json:"name"`
	ConnectorID string `json:"connectorId"`
	Tool        string `json:"tool"`
	Class       string `json:"class"`
	Untrusted   bool   `json:"untrusted"`
	// Results hold what other people wrote (PaluiTool.OthersWrote).
	OthersWrote bool   `json:"-"`
	Title       string `json:"title,omitempty"`
	// Connector label, for search and the capability list.
	Label         string                             `json:"label,omitempty"`
	NeedsApproval func(input json.RawMessage) bool   `json:"-"`
	Preview       func(input json.RawMessage) string `json:"-"`
	// Records the owner's own reply (PaluiTool.OwnerReply).
	OwnerReply bool `json:"-"`
	GuestSafe  bool `json:"guestSafe"`
	// Whose data it touches, as the tool declares it ("" follows the connector).
	Data string `json:"-"`
	// Data resolved against the connector (connectors.ResolveData).
	Scope string `json:"-"`
}

// Collected is CollectedTools. Active grows mid-turn when a search loads tools,
// so it's read and extended through the methods (safe across goroutines).
type Collected struct {
	// Every tool the turn may run, in order: the active ones and the ones search can load.
	Tools *jsonx.Map[connectors.Tool]
	Meta  map[string]*ToolMeta
	// Names not active at the start of the turn, loadable through SearchTool.
	Deferred     []string
	ConnectorIDs []string
	mu           sync.Mutex
	active       []string
}

func emptyCollected() *Collected {
	return &Collected{Tools: jsonx.NewMap[connectors.Tool](), Meta: map[string]*ToolMeta{}, Deferred: []string{}, ConnectorIDs: []string{}, active: []string{}}
}

// Active is the names the model is offered right now.
func (c *Collected) Active() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.active...)
}

func (c *Collected) addActive(names ...string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var fresh []string
	for _, n := range names {
		if !contains(c.active, n) {
			c.active = append(c.active, n)
			fresh = append(fresh, n)
		}
	}
	return fresh
}

// Names is every collected tool name in order (meta key order).
func (c *Collected) Names() []string { return c.Tools.Keys() }

var kindOrder = map[string]int{"skill": 0, "memory": 1, "tools": 2, "channel": 3, "model": 4}

func kindRank(k string) int {
	if r, ok := kindOrder[k]; ok {
		return r
	}
	return 9
}

// Namespaced is namespaced(): <connectorId>__<tool> with every UTF-16 unit
// outside [A-Za-z0-9_-] replaced by "_", cut to 64 characters.
func Namespaced(connectorID, tool string) string {
	var b strings.Builder
	for _, r := range connectorID + ToolSep + tool {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("__")
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// InAudience says whether a tool with this resolved data scope and class may
// be in a turn for this audience. The owner gets everything. A members turn
// also gets the agent's own data, an outside turn only tools whose data stays
// in that chat or that keep nothing; neither ever gets a destructive one or
// shared data. Any other audience or scope gets nothing (fail closed).
func InAudience(audience, scope, class string) bool {
	switch audience {
	case "", AudienceOwner:
		return true
	case AudienceMembers:
		return (scope == connectors.DataNone || scope == connectors.DataChat || scope == connectors.DataAgent) && class != "destructive"
	case AudienceOutside:
		return (scope == connectors.DataChat || scope == connectors.DataNone) && class != "destructive"
	}
	return false
}

// NoApprovals says a turn for this audience has nobody to ask: every audience
// but the owner's. An unknown audience has no tools anyway and must never ask.
func NoApprovals(audience string) bool {
	return audience != "" && audience != AudienceOwner
}

// OutsideApprovalError is what a call gets when its tool asks for approval on
// its own in a turn that has no approvals (members or outside).
const OutsideApprovalError = "This needs the owner's approval, and approvals aren't available in this chat. Don't retry it; say you can't do that here."

// NormalizeClass: anything unclassified is destructive (fail closed).
func NormalizeClass(c string) string {
	if c == "read" || c == "write" || c == "destructive" {
		return c
	}
	return "destructive"
}

type CollectOptions struct {
	Turn     connectors.TurnInfo
	MaxTools int
	// Connector ids allowed (jobs); nil means all.
	Scope []string
	// Namespaced tool names allowed (flow prompt steps); nil or containing "*" means all.
	Only []string
	// Jobs and hooks never get destructive tools.
	ExcludeDestructive bool
	// The agent's tool permissions: tools they turn off are left out.
	Permissions *db.ToolPermissions
	// Also leave out the tools they set to ask (a turn with no approvals).
	HideAsk bool
	// Names loaded earlier in the session, most recent first.
	Sticky []string
	// Called with the names a search just loaded.
	OnLoad func(names []string)
	// Who can read the chat: AudienceOwner ("" too), AudienceMembers or AudienceOutside.
	Audience string
	Log      *logx.Logger
	// Called after an untrusted tool returns (taints the turn).
	OnUntrustedResult func(meta *ToolMeta)
	// Called when any tool starts, for timing.
	OnToolStart func(toolCallID string)
}

func safeTools(ctx context.Context, e connectors.RunningEntry, turn connectors.TurnInfo) (set *connectors.ToolSet, err error) {
	defer func() {
		if r := recover(); r != nil {
			set, err = nil, fmt.Errorf("tools() panicked: %v", r)
		}
	}()
	return e.Handle.Tools(ctx, turn)
}

func isZeroTool(t connectors.Tool) bool {
	return t.Description == "" && t.InputSchema == nil && t.Execute == nil
}

// Collect is collectTools.
func Collect(ctx context.Context, running []connectors.RunningEntry, opts CollectOptions) *Collected {
	out := emptyCollected()
	log := opts.Log
	if log == nil {
		log = logx.Silent
	}
	var entries []connectors.RunningEntry
	for _, e := range running {
		if e.Handle.Tools == nil || (opts.Scope != nil && !contains(opts.Scope, e.ID)) || !connectors.ServesAgent(e.Row, opts.Turn.AgentID) {
			continue
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return kindRank(entries[i].Row.Kind) < kindRank(entries[j].Row.Kind) })
	var order []string
	for _, e := range entries {
		set, err := safeTools(ctx, e, opts.Turn)
		if err != nil {
			log.Warn("connector tools() threw, skipping its tools", logx.Fields{"connector_id": e.ID, "error": err})
			continue
		}
		if set == nil {
			continue
		}
		used := false
		for _, toolName := range set.Keys() {
			pt, _ := set.Get(toolName)
			if isZeroTool(pt.Tool) {
				continue
			}
			cls := NormalizeClass(pt.Class)
			// Before anything lands in order, so search, sticky names and the
			// capability list never see a tool outside the audience.
			if !InAudience(opts.Audience, connectors.ResolveData(pt.Data, e.Row), cls) {
				continue
			}
			if opts.ExcludeDestructive && cls == "destructive" {
				continue
			}
			name := Namespaced(e.ID, toolName)
			if opts.Only != nil && !contains(opts.Only, "*") && !contains(opts.Only, name) {
				continue
			}
			if Hidden(opts.Permissions, name, e.ID, cls, opts.HideAsk) {
				continue
			}
			meta := &ToolMeta{Name: name, ConnectorID: e.ID, Tool: toolName, Class: cls, Untrusted: pt.Untrusted || pt.OthersWrote || e.Row.Trust == "external", OthersWrote: pt.OthersWrote, Title: pt.Title, Label: e.Row.Label, GuestSafe: pt.GuestSafe, Data: pt.Data, Scope: connectors.ResolveData(pt.Data, e.Row), NeedsApproval: pt.NeedsApproval, Preview: pt.Preview, OwnerReply: pt.OwnerReply}
			out.Tools.Set(name, wrapTool(pt.Tool, meta, opts))
			out.Meta[name] = meta
			order = append(order, name)
			used = true
		}
		if used {
			out.ConnectorIDs = append(out.ConnectorIDs, e.ID)
		}
	}
	if len(order) <= opts.MaxTools {
		out.active = append([]string{}, order...)
		return out
	}
	// Over the budget: one slot goes to search, the rest to session-loaded tools
	// first, then by connector kind. Nothing is dropped, the rest is searchable.
	budget := max(1, opts.MaxTools-1)
	var sticky []string
	for _, n := range opts.Sticky {
		if out.Meta[n] != nil {
			sticky = append(sticky, n)
		}
	}
	if len(sticky) > StickyMax {
		sticky = sticky[:StickyMax]
	}
	active := map[string]bool{}
	var activeList []string
	for _, n := range append(sticky, order...) {
		if len(activeList) >= budget {
			break
		}
		if !active[n] {
			active[n] = true
			activeList = append(activeList, n)
		}
	}
	out.active = activeList
	for _, n := range order {
		if !active[n] {
			out.Deferred = append(out.Deferred, n)
		}
	}
	searchMeta := &ToolMeta{Name: SearchTool, ConnectorID: "palui", Tool: "search_tools", Class: "read", Untrusted: false, Title: "Search tools"}
	out.Tools.Set(SearchTool, wrapTool(searchToolFor(out, order, opts), searchMeta, opts))
	out.Meta[SearchTool] = searchMeta
	out.active = append(out.active, SearchTool)
	return out
}

// SearchToolDescription and SearchToolSchema are the meta tool as the TS build
// sends it (the zod schema's JSON form).
const SearchToolDescription = "Find and load tools that are not loaded yet. Search by keywords in any language (what you want to do, a service or server name), or load exact tool names. Loaded tools can be called right after this, and stay loaded for the rest of the conversation."

const SearchToolSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"query":{"description":"Keywords, e.g. 'jadwal sholat' or 'prayer times'","type":"string","maxLength":200},"names":{"description":"Exact tool names to load","maxItems":5,"type":"array","items":{"type":"string","maxLength":100}},"for":{"description":"\"flow\" when picking a tool for a flow step: destructive tools are left out, since flows never run them.","type":"string","enum":["turn","flow"]}},"additionalProperties":false}`

// SharedSearchToolSchema is the search tool in a members or outside turn: no
// "for", since flows and destructive tools never reach that turn.
const SharedSearchToolSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"query":{"description":"Keywords, e.g. 'jadwal sholat' or 'prayer times'","type":"string","maxLength":200},"names":{"description":"Exact tool names to load","maxItems":5,"type":"array","items":{"type":"string","maxLength":100}}},"additionalProperties":false}`

const (
	searchNoMatch       = "No enabled tool matches. Try other words, or tell the owner this isn't available."
	sharedSearchNoMatch = "Nothing here matches. Try other words, or say you can't do that here."
)

type searchInput struct {
	Query *string  `json:"query"`
	Names []string `json:"names"`
	For   string   `json:"for"`
}

// LoadedTool is one entry of the search tool's answer.
type LoadedTool struct {
	Name        string          `json:"name"`
	Connector   string          `json:"connector"`
	Class       string          `json:"class"`
	Untrusted   bool            `json:"untrusted"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input,omitempty"`
}

// SearchResult is the search tool's answer.
type SearchResult struct {
	Loaded []LoadedTool `json:"loaded"`
	Note   string       `json:"note"`
}

// SearchToolZod is the search tool's zod schema; its JSON schema is
// SearchToolSchema (a test keeps the two equal).
var SearchToolZod = zod.Object(
	zod.F("query", zod.String().Max(200).Optional().Describe("Keywords, e.g. 'jadwal sholat' or 'prayer times'")),
	zod.F("names", zod.Array(zod.String().Max(100)).Max(SearchLimit).Optional().Describe("Exact tool names to load")),
	zod.F("for", zod.Enum("turn", "flow").Optional().Describe(`"flow" when picking a tool for a flow step: destructive tools are left out, since flows never run them.`)),
)

// SharedSearchToolZod is SharedSearchToolSchema's zod form (a test keeps the two
// equal).
var SharedSearchToolZod = zod.Object(
	zod.F("query", zod.String().Max(200).Optional().Describe("Keywords, e.g. 'jadwal sholat' or 'prayer times'")),
	zod.F("names", zod.Array(zod.String().Max(100)).Max(SearchLimit).Optional().Describe("Exact tool names to load")),
)

func searchToolFor(out *Collected, order []string, opts CollectOptions) connectors.Tool {
	// A members or outside turn has no flows and no destructive tools, so its
	// search tool doesn't name them, and its no-match note doesn't send the
	// reader to the owner.
	shared := NoApprovals(opts.Audience)
	schema, schemaZod, noMatch := SearchToolSchema, SearchToolZod, searchNoMatch
	if shared {
		schema, schemaZod, noMatch = SharedSearchToolSchema, SharedSearchToolZod, sharedSearchNoMatch
	}
	return connectors.Tool{
		Description: SearchToolDescription,
		InputSchema: json.RawMessage(schema),
		Parse: func(v any) (any, error) {
			parsed, err := zod.Parse(schemaZod, v)
			if err != nil {
				return nil, &zod.TypeValidationError{Value: v, Cause: err}
			}
			return parsed, nil
		},
		Execute: func(ctx context.Context, raw json.RawMessage, call connectors.ToolCallInfo) (any, error) {
			var in searchInput
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("Invalid input for tool %s: %s", SearchTool, err.Error())
			}
			var docs []SearchDoc
			for _, n := range order {
				m := out.Meta[n]
				if !shared && in.For == "flow" && m.Class == "destructive" {
					continue
				}
				label := m.Label
				if label == "" {
					label = m.ConnectorID
				}
				t, _ := out.Tools.Get(n)
				docs = append(docs, SearchDoc{Name: n, ConnectorID: m.ConnectorID, ConnectorLabel: label, Tool: m.Tool, Title: m.Title, Description: t.Description})
			}
			var picked []string
			has := map[string]bool{}
			for _, rawName := range in.Names {
				for _, d := range docs {
					if d.Name == rawName || d.Tool == rawName {
						if !has[d.Name] {
							has[d.Name] = true
							picked = append(picked, d.Name)
						}
						break
					}
				}
			}
			query := ""
			if in.Query != nil {
				query = *in.Query
			}
			for _, h := range SearchTools(docs, query, SearchLimit) {
				if len(picked) >= SearchLimit {
					break
				}
				if !has[h.Doc.Name] {
					has[h.Doc.Name] = true
					picked = append(picked, h.Doc.Name)
				}
			}
			fresh := out.addActive(picked...)
			if len(picked) > 0 && opts.OnLoad != nil {
				opts.OnLoad(picked)
			}
			if len(fresh) > 0 && opts.Log != nil {
				opts.Log.Info("tool search loaded tools", logx.Fields{"query": query, "tools": fresh})
			}
			if len(picked) == 0 {
				return SearchResult{Loaded: []LoadedTool{}, Note: noMatch}, nil
			}
			loaded := make([]LoadedTool, 0, len(picked))
			for _, n := range picked {
				m := out.Meta[n]
				t, _ := out.Tools.Get(n)
				label := m.Label
				if label == "" {
					label = m.ConnectorID
				}
				loaded = append(loaded, LoadedTool{Name: n, Connector: label, Class: m.Class, Untrusted: m.Untrusted, Description: t.Description, Input: t.InputSchema})
			}
			return SearchResult{Loaded: loaded, Note: "These tools are loaded. Call them directly."}, nil
		},
	}
}

// wrapTool times the call and, for untrusted tools, taints the turn after it
// returns (even with an error, which may carry the content). In a members or
// outside turn, a call whose tool asks for approval on its own is refused:
// that turn has no approvals.
func wrapTool(t connectors.Tool, meta *ToolMeta, opts CollectOptions) connectors.Tool {
	exec := t.Execute
	if exec == nil {
		return t
	}
	t.Execute = func(ctx context.Context, input json.RawMessage, call connectors.ToolCallInfo) (any, error) {
		if opts.OnToolStart != nil {
			opts.OnToolStart(call.ToolCallID)
		}
		if NoApprovals(opts.Audience) && AsksOnItsOwn(meta.NeedsApproval, input) {
			return nil, errors.New(OutsideApprovalError)
		}
		defer func() {
			if meta.Untrusted && opts.OnUntrustedResult != nil {
				opts.OnUntrustedResult(meta)
			}
		}()
		return exec(ctx, input, call)
	}
	return t
}

// AsksOnItsOwn is the tool's NeedsApproval: false when the tool has none, and
// a panic counts as asking.
func AsksOnItsOwn(fn func(json.RawMessage) bool, input json.RawMessage) (ask bool) {
	if fn == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			ask = true
		}
	}()
	return fn(input)
}
