package proactive

// Mentions (RFC 3): a prompt names the tools a run may call as
// @<connectorId>__<tool>, the namespaced name the model sees. The mentions are
// the run's whole tool scope. Each must resolve to a read tool the agent may
// use, checked at save and at every run; one that doesn't fails the run.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/agents"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/logx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// mentionRe is a mention: "@" then a namespaced tool name. A mention must
// start the text or follow a character that can't be part of a name, so an
// email address isn't read as one.
var mentionRe = regexp.MustCompile(`(^|[^A-Za-z0-9_.@-])@([A-Za-z0-9][A-Za-z0-9_-]*__[A-Za-z0-9][A-Za-z0-9_-]*)`)

// Mentions lists a prompt's mentioned tool names, in order, each once.
func Mentions(prompt string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(prompt, -1) {
		name := strings.TrimRight(m[2], "-_")
		if len(name) > 64 || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// ToolInfo is one tool an agent may use, as the editor and the checks see it.
type ToolInfo struct {
	Name           string `json:"name"`
	ConnectorID    string `json:"connectorId"`
	ConnectorLabel string `json:"connectorLabel"`
	Tool           string `json:"tool"`
	Class          string `json:"class"`
	// An MCP tool the owner hasn't classified yet (Class is destructive
	// until then): what the server suggests.
	SuggestedClass string  `json:"suggestedClass,omitempty"`
	Title          *string `json:"title"`
	Untrusted      bool    `json:"untrusted"`
}

// Tool is a mentioned tool opened for a run.
type Tool struct {
	Info          ToolInfo
	Description   string
	Schema        json.RawMessage
	Execute       func(ctx context.Context, input json.RawMessage) (any, error)
	ToModelOutput func(output any) models.ToolOutput
}

// ToolHost lists the tools an agent may use and opens them for a run.
// LiveTools is the real one; tests fake it.
type ToolHost interface {
	Tools(ctx context.Context, agentID, chatID string, now int64) ([]Tool, error)
}

var (
	hostMu      sync.RWMutex
	defaultHost ToolHost
)

// SetToolHost sets the host Save and the chat tools use to check mentions
// when they aren't given one (serve wires the live one).
func SetToolHost(h ToolHost) {
	hostMu.Lock()
	defaultHost = h
	hostMu.Unlock()
}

// DefaultToolHost is what SetToolHost set (nil before).
func DefaultToolHost() ToolHost {
	hostMu.RLock()
	defer hostMu.RUnlock()
	return defaultHost
}

// LiveTools collects tools from the running connectors the way an unattended
// turn does: the agent's connectors, its tool permissions, nothing that would
// need an approval.
type LiveTools struct {
	DB      db.Q
	Running func() []connectors.RunningEntry
	Log     *logx.Logger
}

func (l LiveTools) Tools(ctx context.Context, agentID, chatID string, now int64) ([]Tool, error) {
	if l.Running == nil {
		return nil, errors.New("No connectors are running")
	}
	agent, ok, err := agents.Get(l.DB, agentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, bad("That agent doesn't exist")
	}
	tz, err := settings.Timezone(l.DB)
	if err != nil || tz == "" {
		tz = "UTC"
	}
	perms := agent.ToolPermissions.V
	all := tools.Collect(ctx, l.Running(), tools.CollectOptions{
		Turn:              connectors.TurnInfo{ChatID: chatID, Trigger: "schedule", Origin: "schedule", TZ: tz, Now: now, AgentID: agentID, Mode: tools.AudienceOwner},
		MaxTools:          math.MaxInt,
		Permissions:       &perms,
		HideAsk:           true,
		Log:               l.Log,
		OnUntrustedResult: func(*tools.ToolMeta) {},
	})
	var out []Tool
	for _, name := range all.Names() {
		m := all.Meta[name]
		if m == nil || m.Name == tools.SearchTool {
			continue
		}
		t, _ := all.Tools.Get(name)
		var title *string
		if m.Title != "" {
			s := m.Title
			title = &s
		}
		label := m.Label
		if label == "" {
			label = m.ConnectorID
		}
		info := ToolInfo{Name: m.Name, ConnectorID: m.ConnectorID, ConnectorLabel: label, Tool: m.Tool, Class: m.Class, SuggestedClass: m.Suggested, Title: title, Untrusted: m.Untrusted}
		tool := Tool{Info: info, Description: t.Description, Schema: t.InputSchema, ToModelOutput: t.ToModelOutput}
		if exec := t.Execute; exec != nil {
			tool.Execute = func(ctx context.Context, input json.RawMessage) (any, error) {
				return exec(ctx, input, connectors.ToolCallInfo{ToolCallID: "proactive"})
			}
		}
		out = append(out, tool)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Info.Name < out[j].Info.Name })
	return out, nil
}

// MentionProblem is one mention that doesn't resolve.
type MentionProblem struct {
	Mention string `json:"mention"`
	Reason  string `json:"reason"`
}

// Problem reasons, shown on the tab and to the model as is.
const (
	ReasonNotFound   = "isn't available: its connector is off, removed or not allowed for this agent, or it has no such tool."
	ReasonNotRead    = "can change things. Proactive can only use read tools."
	ReasonUnsorted   = "isn't classified yet. Classify it as read on the Connectors page."
	ReasonNoMentions = "Mention at least one tool in the prompt, like @gcal__get_calendar."
)

// Check says which mentions don't resolve to a read tool the agent may use.
// Nothing mentioned is an error too: a run with no tools has nothing to read.
func Check(mentions []string, available []ToolInfo) error {
	if len(mentions) == 0 {
		return bad(ReasonNoMentions)
	}
	byName := map[string]ToolInfo{}
	for _, t := range available {
		byName[t.Name] = t
	}
	var problems []MentionProblem
	for _, m := range mentions {
		if r := problemWith(byName, m); r != "" {
			problems = append(problems, MentionProblem{m, r})
		}
	}
	if len(problems) > 0 {
		lines := make([]string, len(problems))
		for i, p := range problems {
			lines[i] = "@" + p.Mention + " " + p.Reason
		}
		return bad("%s", strings.Join(lines, " "))
	}
	return nil
}

// MentionStatus is each mention with what's wrong with it ("" when fine),
// for the editor.
func MentionStatus(mentions []string, available []ToolInfo) []MentionProblem {
	byName := map[string]ToolInfo{}
	for _, t := range available {
		byName[t.Name] = t
	}
	out := make([]MentionProblem, len(mentions))
	for i, m := range mentions {
		out[i] = MentionProblem{m, problemWith(byName, m)}
	}
	return out
}

func problemWith(byName map[string]ToolInfo, m string) string {
	t, ok := byName[m]
	switch {
	case !ok:
		return ReasonNotFound
	case t.Class == "read":
		return ""
	case t.SuggestedClass != "":
		return ReasonUnsorted
	}
	return ReasonNotRead
}

// Infos is the info of each tool.
func Infos(list []Tool) []ToolInfo {
	out := make([]ToolInfo, len(list))
	for i, t := range list {
		out[i] = t.Info
	}
	return out
}

// Resolve opens the mentioned tools for a run, or says which ones don't
// resolve.
func Resolve(mentions []string, available []Tool) (map[string]Tool, error) {
	var runnable []Tool
	for _, t := range available {
		if t.Execute != nil {
			runnable = append(runnable, t)
		}
	}
	if err := Check(mentions, Infos(runnable)); err != nil {
		return nil, err
	}
	out := map[string]Tool{}
	for _, t := range runnable {
		for _, m := range mentions {
			if t.Info.Name == m {
				out[m] = t
			}
		}
	}
	return out, nil
}
