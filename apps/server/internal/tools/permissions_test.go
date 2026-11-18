package tools_test

import (
	"reflect"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// Ports packages/core/src/tools/permissions.test.ts.

func perms(allowAll bool, kv ...string) *db.ToolPermissions {
	m := jsonx.NewMap[string]()
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return &db.ToolPermissions{AllowAll: allowAll, Tools: m}
}

func TestReadsAStoredValueAndDropsWhatItDoesntKnow(t *testing.T) {
	if got := jsonx.Stringify(tools.ReadPermissions([]byte("null"))); got != `{"allowAll":false,"tools":{}}` {
		t.Fatal(got)
	}
	got := tools.ReadPermissions([]byte(`{"allowAll":true,"tools":{"a__x":"allow","bad name":"allow","a__y":"maybe","a__z":"deny"}}`))
	if jsonx.Stringify(got) != `{"allowAll":true,"tools":{"a__x":"allow","a__z":"deny"}}` {
		t.Fatal(jsonx.Stringify(got))
	}
}

func TestAToolsOwnEntryWinsOverAllowAll(t *testing.T) {
	p := perms(true, "a__x", "ask")
	if tools.PermissionFor(p, "a__x") != "ask" || tools.PermissionFor(p, "a__y") != "allow" {
		t.Fatal("allowAll")
	}
	if tools.PermissionFor(perms(false), "a__y") != "" || tools.PermissionFor(nil, "a__y") != "" {
		t.Fatal("none")
	}
}

func TestListsDeniedTools(t *testing.T) {
	if got := tools.DeniedTools(perms(false, "a__x", "deny", "a__y", "allow")); !reflect.DeepEqual(got, []string{"a__x"}) {
		t.Fatal(got)
	}
	if got := tools.DeniedTools(nil); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestAllowSkipsThePromptOnlyInAnOwnerTurn(t *testing.T) {
	base := tools.ApprovalInput{Class: "destructive"}
	cases := []struct {
		in   tools.ApprovalInput
		want bool
	}{
		{tools.ApprovalInput{Class: "destructive", OwnerTurn: true}, true},
		{tools.ApprovalInput{Class: base.Class, Permission: "allow", OwnerTurn: true}, false},
		{tools.ApprovalInput{Class: base.Class, Permission: "allow"}, true},
		{tools.ApprovalInput{Class: "write", Permission: "allow", OwnerTurn: true, Tainted: true, Strict: true}, false},
		{tools.ApprovalInput{Class: "write", Permission: "allow", Strict: true}, true},
		// ask always asks, even for a read.
		{tools.ApprovalInput{Class: "read", Permission: "ask", OwnerTurn: true}, true},
		// no entry keeps the class rule.
		{tools.ApprovalInput{Class: "read", OwnerTurn: true, Tainted: true, Strict: true}, false},
		{tools.ApprovalInput{Class: "write", OwnerTurn: true}, false},
		{tools.ApprovalInput{Class: "write", OwnerTurn: true, Tainted: true}, true},
		// a tainted turn always asks before sending a saved file.
		{tools.ApprovalInput{Class: "read", OwnerTurn: true, SendsFile: true, Tainted: true}, true},
		{tools.ApprovalInput{Class: "write", Permission: "allow", OwnerTurn: true, SendsFile: true, Tainted: true}, true},
		{tools.ApprovalInput{Class: "read", OwnerTurn: true, SendsFile: true}, false},
		{tools.ApprovalInput{Class: "write", Permission: "allow", OwnerTurn: true, SendsFile: true}, false},
		// after reading what other people wrote, a write asks even when allowed.
		{tools.ApprovalInput{Class: "write", Permission: "allow", OwnerTurn: true, Tainted: true, OthersWrote: true}, true},
		{tools.ApprovalInput{Class: "destructive", Permission: "allow", OwnerTurn: true, Tainted: true, OthersWrote: true}, true},
		{tools.ApprovalInput{Class: "read", Permission: "allow", OwnerTurn: true, Tainted: true, OthersWrote: true}, false},
		{tools.ApprovalInput{Class: "read", OwnerTurn: true, Tainted: true, OthersWrote: true}, false},
	}
	for i, c := range cases {
		if got := tools.NeedsApproval(c.in); got != c.want {
			t.Errorf("case %d %+v: %v", i, c.in, got)
		}
	}
}

func TestAnOwnerReplyIgnoresOnlyReplayTaintInAnOwnerTurn(t *testing.T) {
	w := tools.ApprovalInput{Class: "write", OwnerReply: true}
	cases := []struct {
		name string
		in   func(tools.ApprovalInput) tools.ApprovalInput
		want bool
	}{
		{"owner turn, untrusted call in history", func(i tools.ApprovalInput) tools.ApprovalInput { i.OwnerTurn, i.ReplayTainted = true, true; return i }, false},
		{"owner turn, clean", func(i tools.ApprovalInput) tools.ApprovalInput { i.OwnerTurn = true; return i }, false},
		{"owner turn, untrusted call in this turn", func(i tools.ApprovalInput) tools.ApprovalInput {
			i.OwnerTurn, i.Tainted, i.ReplayTainted = true, true, true
			return i
		}, true},
		{"owner turn, others wrote", func(i tools.ApprovalInput) tools.ApprovalInput {
			i.OwnerTurn, i.ReplayTainted, i.OthersWrote = true, true, true
			return i
		}, true},
		{"job, flow or hook", func(i tools.ApprovalInput) tools.ApprovalInput { i.Strict = true; return i }, true},
		{"not an owner turn, replay tainted", func(i tools.ApprovalInput) tools.ApprovalInput { i.ReplayTainted = true; return i }, true},
		{"set to ask", func(i tools.ApprovalInput) tools.ApprovalInput { i.OwnerTurn, i.Permission = true, "ask"; return i }, true},
		{"any other write after replay taint", func(i tools.ApprovalInput) tools.ApprovalInput {
			i.OwnerReply, i.OwnerTurn, i.ReplayTainted = false, true, true
			return i
		}, true},
	}
	for _, c := range cases {
		if got := tools.NeedsApproval(c.in(w)); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestListsAskTools(t *testing.T) {
	if got := tools.AskTools(perms(true, "a__x", "deny", "a__y", "ask", "a__z", "allow", "a__w", "ask")); !reflect.DeepEqual(got, []string{"a__y", "a__w"}) {
		t.Fatal(got)
	}
	if got := tools.AskTools(nil); len(got) != 0 {
		t.Fatal(got)
	}
}
