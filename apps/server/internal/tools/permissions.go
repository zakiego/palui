// Package tools ports packages/core/src/tools. M1 has permissions.ts: per-agent
// tool permissions. The owner can let an agent run every tool without asking,
// or pick allow / ask / deny per tool. They only loosen turns the owner
// started. Jobs, flows and hooks keep the class rules. Shared chats have no
// approvals: "ask" and "deny" tools are left out there (PLAN 19.4).
package tools

import (
	"encoding/json"
	"regexp"
	"slices"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Permissions are the values one tool can have.
var Permissions = []string{"allow", "ask", "deny"}

// MaxToolPermissions is the entries a policy keeps.
const MaxToolPermissions = 2000

// ToolName is a namespaced tool name.
var ToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

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
	if t, ok := obj.Get("tools"); ok {
		if tools, ok := t.(*jsonx.Object); ok {
			for _, name := range tools.Keys() {
				p, _ := tools.Get(name)
				ps, isString := p.(string)
				if ToolName.MatchString(name) && isString && slices.Contains(Permissions, ps) {
					out.Tools.Set(name, ps)
				}
			}
		}
	}
	return out
}

// ReadPermissionsValue is ReadPermissions for an already decoded value.
func ReadPermissionsValue(v any) db.ToolPermissions {
	b, err := json.Marshal(v)
	if err != nil {
		return NoPermissions()
	}
	return ReadPermissions(b)
}

// PermissionFor is what the owner picked for one tool: its own entry, else
// "allow" when AllowAll is on, else "" (the class rule decides).
func PermissionFor(p *db.ToolPermissions, name string) string {
	if p == nil {
		return ""
	}
	if own, ok := p.Tools.Get(name); ok && own != "" {
		return own
	}
	if p.AllowAll {
		return "allow"
	}
	return ""
}

// DeniedTools are the names hidden from the agent in every turn.
func DeniedTools(p *db.ToolPermissions) []string {
	out := []string{}
	if p == nil {
		return out
	}
	for _, k := range p.Tools.Keys() {
		if v, _ := p.Tools.Get(k); v == "deny" {
			out = append(out, k)
		}
	}
	return out
}

// AskTools are the names the owner set to "ask". An outside turn leaves them
// out: it has no approvals, and the owner wanted a say on these.
func AskTools(p *db.ToolPermissions) []string {
	out := []string{}
	if p == nil {
		return out
	}
	for _, k := range p.Tools.Keys() {
		if v, _ := p.Tools.Get(k); v == "ask" {
			out = append(out, k)
		}
	}
	return out
}

type ApprovalInput struct {
	// "read", "write" or "destructive".
	Class string
	// "allow", "ask", "deny" or "".
	Permission string
	// The owner started this turn and is there to see it.
	OwnerTurn bool
	Tainted   bool
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
	// A turn that read something untrusted never sends a saved file on its own.
	if i.SendsFile && i.Tainted {
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
	return i.Class == "write" && (i.Tainted || i.Strict)
}
