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
}

// NeedsApproval says whether a call waits for the owner's yes.
func NeedsApproval(i ApprovalInput) bool {
	if i.Permission == "ask" {
		return true
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
		return true
	}
	// Anyone in another agent's chat can plant text for the owner's agent to
	// read, so after that no write runs on its own, "allow" included.
	if i.OthersWrote && (i.Class == "write" || i.Class == "destructive") {
		return true
	}
	if i.Permission == "allow" && i.OwnerTurn {
		return false
	}
	if i.Class == "destructive" {
		return true
	}
	return i.Class == "write" && (tainted || i.Strict)
}
