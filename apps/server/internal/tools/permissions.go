// Package tools ports packages/core/src/tools. M1 has permissions.ts: per-agent
// tool permissions. The owner can let an agent run every tool without asking,
// or pick allow / ask / deny per connector, per class within a connector
// (read, write, destructive) and per tool. They only loosen turns the owner
// started. Jobs, flows and hooks keep the class rules. Shared chats have no
// approvals: "ask" and "deny" tools are left out there (PLAN 19.4).
package tools

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Permissions are the values one tool can have.
var Permissions = []string{"allow", "ask", "deny"}

// MaxToolPermissions is the entries a policy keeps (tools, classes and
// connectors together).
const MaxToolPermissions = 2000

// ToolName is a namespaced tool name.
var ToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Classes are the tool classes a connector's tools are grouped by.
var Classes = []string{"read", "write", "destructive"}

// MaxConnectorIDLen is the longest connector id a policy keeps.
const MaxConnectorIDLen = 100

// ValidConnectorKey is a connector id a policy can hold an entry for.
func ValidConnectorKey(id string) bool {
	return id != "" && len(id) <= MaxConnectorIDLen && strings.TrimSpace(id) == id
}

// ClassKey is the Classes key for one class of one connector.
func ClassKey(connectorID, class string) string {
	return connectorID + ":" + class
}

// ValidClassKey is "<connectorId>:<class>" with a known class.
func ValidClassKey(key string) bool {
	i := strings.LastIndex(key, ":")
	return i > 0 && ValidConnectorKey(key[:i]) && slices.Contains(Classes, key[i+1:])
}

// NoPermissions is the empty policy.
func NoPermissions() db.ToolPermissions {
	return db.ToolPermissions{AllowAll: false, Tools: jsonx.NewMap[string]()}
}

// ReadPermissions fixes up a stored value: unknown entries and malformed names are dropped.
func ReadPermissions(raw []byte) db.ToolPermissions {
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return NoPermissions()
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok {
		return NoPermissions()
	}
	out := NoPermissions()
	if v, ok := obj.Get("allowAll"); ok && v == true {
		out.AllowAll = true
	}
	if v, ok := obj.Get("askAfterUntrusted"); ok && v == true {
		out.AskAfterUntrusted = true
	}
	read := func(field string, valid func(string) bool) *jsonx.Map[string] {
		m := jsonx.NewMap[string]()
		if t, ok := obj.Get(field); ok {
			if entries, ok := t.(*jsonx.Object); ok {
				for _, k := range entries.Keys() {
					p, _ := entries.Get(k)
					ps, isString := p.(string)
					if valid(k) && isString && slices.Contains(Permissions, ps) {
						m.Set(k, ps)
					}
				}
			}
		}
		return m
	}
	out.Tools = read("tools", ToolName.MatchString)
	if m := read("connectors", ValidConnectorKey); m.Len() > 0 {
		out.Connectors = m
	}
	if m := read("classes", ValidClassKey); m.Len() > 0 {
		out.Classes = m
	}
	return out
}

// Entries is how many entries a policy holds across its three maps.
func Entries(p db.ToolPermissions) int {
	return p.Tools.Len() + p.Connectors.Len() + p.Classes.Len()
}

// ReadPermissionsValue is ReadPermissions for an already decoded value.
func ReadPermissionsValue(v any) db.ToolPermissions {
	b, err := json.Marshal(v)
	if err != nil {
		return NoPermissions()
	}
	return ReadPermissions(b)
}

// PermissionFor is what the owner picked for one tool. A higher level locks
// the ones under it: its connector's entry, then its class in that
// connector, then the tool's own, else "allow" when AllowAll is on, else ""
// (the class rule decides). class is the tool's class.
func PermissionFor(p *db.ToolPermissions, name, connectorID, class string) string {
	if p == nil {
		return ""
	}
	if connectorID != "" {
		if v, ok := p.Connectors.Get(connectorID); ok && v != "" {
			return v
		}
		if v, ok := p.Classes.Get(ClassKey(connectorID, NormalizeClass(class))); ok && v != "" {
			return v
		}
	}
	if own, ok := p.Tools.Get(name); ok && own != "" {
		return own
	}
	if p.AllowAll {
		return "allow"
	}
	return ""
}

// Hidden says whether a tool is left out of a turn: the owner turned it off,
// or set it to ask in a turn that has no approvals (open).
func Hidden(p *db.ToolPermissions, name, connectorID, class string, open bool) bool {
	switch PermissionFor(p, name, connectorID, class) {
	case "deny":
		return true
	case "ask":
		return open
	}
	return false
}

type ApprovalInput struct {
	// "read", "write" or "destructive".
	Class string
	// "allow", "ask", "deny" or "".
	Permission string
	// The owner started this turn and is there to see it.
	OwnerTurn bool
	// An untrusted result came back earlier in this turn.
	Tainted bool
	// An untrusted result is in the history replayed to this turn.
	ReplayTainted bool
	// The tool only records the owner's own reply in this chat (PaluiTool.OwnerReply).
	OwnerReply bool
	// The turn read what other people wrote (another agent's chats or memory).
	OthersWrote bool
	// Jobs, flows and hooks: every write asks.
	Strict bool
	// The call carries a saved file (a palui:attachment ref).
	SendsFile bool
	// The agent's "ask before writes after reading untrusted content" switch
	// (ToolPermissions.AskAfterUntrusted): owner turns keep the taint rules.
	AskAfterUntrusted bool
}

// Why a call waits for the owner. The codes are stored with the request
// (pending_approvals.reason) and shown to the owner and the model.
const (
	// The owner set this tool, its group or its connector to "ask".
	ReasonSetToAsk = "set-to-ask"
	// The tool's own rule asks for this call (PaluiTool.NeedsApproval).
	ReasonToolRule = "tool-rule"
	// Destructive tools ask unless the owner set them to "allow".
	ReasonDestructive = "destructive"
	// A job, flow, watch or hook: nobody is watching, so every write asks.
	ReasonUnattended = "unattended"
	// The turn, or the history it replays, read untrusted content.
	ReasonUntrusted = "untrusted"
	// The turn read what other people wrote (another agent's chats or memory).
	ReasonOthersWrote = "others-wrote"
	// A saved file would leave after the turn read untrusted content.
	ReasonSendsFile = "sends-file"
)

// ReasonText is what the model and the dashboard are told about a reason code.
func ReasonText(code string) string {
	switch code {
	case ReasonSetToAsk:
		return "the owner set this tool to ask first in the agent's tool settings"
	case ReasonToolRule:
		return "this tool's own setting asks before this kind of call"
	case ReasonDestructive:
		return "it is a destructive tool, and those always ask unless the owner sets them to allow"
	case ReasonUnattended:
		return "this turn runs unattended (a job, flow, watch or hook), so every write asks"
	case ReasonUntrusted:
		return "this conversation read untrusted content earlier, and the agent is set to ask before writes after that"
	case ReasonOthersWrote:
		return "this conversation read what other people wrote, and the agent is set to ask before writes after that"
	case ReasonSendsFile:
		return "the call sends a saved file after this conversation read untrusted content, and the agent is set to ask before that"
	}
	return "the approval rules asked for it"
}

// NeedsApproval says whether a call waits for the owner's yes.
func NeedsApproval(i ApprovalInput) bool {
	return ApprovalReason(i) != ""
}

// ApprovalReason is NeedsApproval with the reason: "" when the call runs on
// its own, else one of the Reason codes.
//
// In the owner's own turn the answer depends only on the agent's policy and
// the tool's class, never on what the conversation happened to read: "ask"
// asks, "allow" runs, and with no entry read and write run while destructive
// asks. The same tool on the same agent gets the same answer every time. The
// taint rules (untrusted content, others' content, sending a saved file after
// either) apply there only when the agent's AskAfterUntrusted switch is on.
// Jobs, flows, watches and hooks keep every rule: nobody is there to notice.
func ApprovalReason(i ApprovalInput) string {
	if i.Permission == "ask" {
		return ReasonSetToAsk
	}
	if i.OwnerTurn && !i.Strict && !i.AskAfterUntrusted {
		if i.Permission == "allow" || i.Class != "destructive" {
			return ""
		}
		return ReasonDestructive
	}
	tainted := i.Tainted || i.ReplayTainted
	// Recording the owner's answer to a check-in, in the owner's own turn, is
	// the owner's own words, not something an old web page or chat log asked
	// for. Untrusted text from this same turn still makes it ask.
	if i.OwnerReply && i.OwnerTurn && !i.Strict {
		tainted = i.Tainted
	}
	// A turn that read something untrusted never sends a saved file on its own.
	if i.SendsFile && tainted {
		return ReasonSendsFile
	}
	// Anyone in another agent's chat can plant text for the owner's agent to
	// read, so after that no write runs on its own, "allow" included.
	if i.OthersWrote && (i.Class == "write" || i.Class == "destructive") {
		return ReasonOthersWrote
	}
	if i.Permission == "allow" && i.OwnerTurn {
		return ""
	}
	if i.Class == "destructive" {
		return ReasonDestructive
	}
	if i.Class == "write" && i.Strict {
		return ReasonUnattended
	}
	if i.Class == "write" && tainted {
		return ReasonUntrusted
	}
	return ""
}
