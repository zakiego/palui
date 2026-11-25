package tools_test

import (
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

func TestReadsConnectorAndClassEntries(t *testing.T) {
	got := tools.ReadPermissions([]byte(`{"allowAll":false,"tools":{},"connectors":{"a":"allow"," b":"allow","c":"maybe"},"classes":{"a:write":"ask","a:other":"deny",":read":"deny","x:y:read":"deny"}}`))
	if jsonx.Stringify(got) != `{"allowAll":false,"tools":{},"connectors":{"a":"allow"},"classes":{"a:write":"ask","x:y:read":"deny"}}` {
		t.Fatal(jsonx.Stringify(got))
	}
	// Nothing at those levels keeps the old shape.
	if got := jsonx.Stringify(tools.ReadPermissions([]byte(`{"allowAll":true,"tools":{},"connectors":{},"classes":{"a:bad":"deny"}}`))); got != `{"allowAll":true,"tools":{}}` {
		t.Fatal(got)
	}
}

func TestAToolsOwnEntryWinsOverAllowAll(t *testing.T) {
	p := perms(true, "a__x", "ask")
	if tools.PermissionFor(p, "a__x", "a", "read") != "ask" || tools.PermissionFor(p, "a__y", "a", "read") != "allow" {
		t.Fatal("allowAll")
	}
	if tools.PermissionFor(perms(false), "a__y", "a", "read") != "" || tools.PermissionFor(nil, "a__y", "a", "read") != "" {
		t.Fatal("none")
	}
}

func TestAHigherLevelLocksTheOnesUnderIt(t *testing.T) {
	p := perms(false, "a__x", "allow", "b__x", "deny", "b__y", "allow")
	p.Connectors = jsonx.NewMap[string]()
	p.Connectors.Set("a", "deny")
	p.Classes = jsonx.NewMap[string]()
	p.Classes.Set("a:write", "ask")
	p.Classes.Set("b:write", "ask")
	p.Classes.Set("b:destructive", "allow")
	cases := []struct {
		name, tool, connector, class, want string
	}{
		{"connector over tool", "a__x", "a", "write", "deny"},
		{"connector over class", "a__y", "a", "write", "deny"},
		{"class over tool", "b__x", "b", "write", "ask"},
		{"unclassified is destructive", "b__z", "b", "", "allow"},
		{"tool when nothing above", "b__y", "b", "read", "allow"},
		{"nothing set", "c__r", "c", "write", ""},
		{"no connector id", "b__x", "", "write", "deny"},
	}
	for _, c := range cases {
		if got := tools.PermissionFor(p, c.tool, c.connector, c.class); got != c.want {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	// AllowAll is only the fallback.
	p.AllowAll = true
	if got := tools.PermissionFor(p, "a__r", "a", "read"); got != "deny" {
		t.Fatal(got)
	}
	if got := tools.PermissionFor(p, "c__r", "c", "read"); got != "allow" {
		t.Fatal(got)
	}
}

func TestHidesOffToolsAndAskToolsWithoutApprovals(t *testing.T) {
	p := perms(true, "a__x", "deny", "a__y", "ask", "a__z", "allow")
	p.Classes = jsonx.NewMap[string]()
	p.Classes.Set("b:write", "ask")
	cases := []struct {
		tool, connector, class string
		open, want             bool
	}{
		{"a__x", "a", "read", false, true},
		{"a__y", "a", "read", false, false},
		{"a__y", "a", "read", true, true},
		{"a__z", "a", "read", true, false},
		{"b__w", "b", "write", true, true},
		{"b__w", "b", "write", false, false},
		{"b__r", "b", "read", true, false},
	}
	for _, c := range cases {
		if got := tools.Hidden(p, c.tool, c.connector, c.class, c.open); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
	if tools.Hidden(nil, "a__x", "a", "read", true) {
		t.Fatal("nil")
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
		{tools.ApprovalInput{Class: "write", OwnerTurn: true, AskAfterUntrusted: true, Tainted: true}, true},
		// a tainted turn always asks before sending a saved file.
		{tools.ApprovalInput{Class: "read", OwnerTurn: true, AskAfterUntrusted: true, SendsFile: true, Tainted: true}, true},
		{tools.ApprovalInput{Class: "write", Permission: "allow", OwnerTurn: true, AskAfterUntrusted: true, SendsFile: true, Tainted: true}, true},
		{tools.ApprovalInput{Class: "read", OwnerTurn: true, AskAfterUntrusted: true, SendsFile: true}, false},
		{tools.ApprovalInput{Class: "write", Permission: "allow", OwnerTurn: true, AskAfterUntrusted: true, SendsFile: true}, false},
		// after reading what other people wrote, a write asks even when allowed.
		{tools.ApprovalInput{Class: "write", Permission: "allow", OwnerTurn: true, AskAfterUntrusted: true, Tainted: true, OthersWrote: true}, true},
		{tools.ApprovalInput{Class: "destructive", Permission: "allow", OwnerTurn: true, AskAfterUntrusted: true, Tainted: true, OthersWrote: true}, true},
		{tools.ApprovalInput{Class: "read", Permission: "allow", OwnerTurn: true, AskAfterUntrusted: true, Tainted: true, OthersWrote: true}, false},
		{tools.ApprovalInput{Class: "read", OwnerTurn: true, AskAfterUntrusted: true, Tainted: true, OthersWrote: true}, false},
	}
	for i, c := range cases {
		if got := tools.NeedsApproval(c.in); got != c.want {
			t.Errorf("case %d %+v: %v", i, c.in, got)
		}
	}
}

func TestAnOwnerReplyIgnoresOnlyReplayTaintInAnOwnerTurn(t *testing.T) {
	w := tools.ApprovalInput{Class: "write", OwnerReply: true, AskAfterUntrusted: true}
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

// The owner's own turn: the answer depends on the policy and the class only,
// whatever the conversation read before (the bug where the same tool ran once
// and asked the next time).
func TestAnOwnerTurnDecidesFromThePolicyAndClassOnly(t *testing.T) {
	type taint struct{ tainted, replay, others, file bool }
	histories := []taint{{}, {tainted: true}, {replay: true}, {others: true}, {replay: true, others: true}, {tainted: true, file: true}, {replay: true, file: true}}
	cases := []struct {
		class, permission, want string
	}{
		{"read", "", ""},
		{"write", "", ""},
		{"destructive", "", tools.ReasonDestructive},
		{"read", "allow", ""},
		{"write", "allow", ""},
		{"destructive", "allow", ""},
		{"read", "ask", tools.ReasonSetToAsk},
		{"write", "ask", tools.ReasonSetToAsk},
		{"destructive", "ask", tools.ReasonSetToAsk},
	}
	for _, c := range cases {
		for _, h := range histories {
			for _, ownerReply := range []bool{false, true} {
				got := tools.ApprovalReason(tools.ApprovalInput{
					Class: c.class, Permission: c.permission, OwnerTurn: true, OwnerReply: ownerReply,
					Tainted: h.tainted, ReplayTainted: h.replay, OthersWrote: h.others, SendsFile: h.file,
				})
				if got != c.want {
					t.Errorf("%s/%q after %+v (owner reply %v): %q, want %q", c.class, c.permission, h, ownerReply, got, c.want)
				}
			}
		}
	}
}

func TestApprovalReasons(t *testing.T) {
	cases := []struct {
		name string
		in   tools.ApprovalInput
		want string
	}{
		{"job write", tools.ApprovalInput{Class: "write", Strict: true}, tools.ReasonUnattended},
		{"job read", tools.ApprovalInput{Class: "read", Strict: true}, ""},
		{"job destructive", tools.ApprovalInput{Class: "destructive", Strict: true}, tools.ReasonDestructive},
		{"job file after taint", tools.ApprovalInput{Class: "read", Strict: true, Tainted: true, SendsFile: true}, tools.ReasonSendsFile},
		{"switch on, tainted write", tools.ApprovalInput{Class: "write", OwnerTurn: true, AskAfterUntrusted: true, ReplayTainted: true}, tools.ReasonUntrusted},
		{"switch on, others wrote", tools.ApprovalInput{Class: "write", Permission: "allow", OwnerTurn: true, AskAfterUntrusted: true, OthersWrote: true}, tools.ReasonOthersWrote},
		{"switch on, file after taint", tools.ApprovalInput{Class: "read", OwnerTurn: true, AskAfterUntrusted: true, Tainted: true, SendsFile: true}, tools.ReasonSendsFile},
		{"switch on, clean write", tools.ApprovalInput{Class: "write", OwnerTurn: true, AskAfterUntrusted: true}, ""},
		{"set to ask", tools.ApprovalInput{Class: "read", Permission: "ask"}, tools.ReasonSetToAsk},
	}
	for _, c := range cases {
		if got := tools.ApprovalReason(c.in); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if tools.NeedsApproval(c.in) != (c.want != "") {
			t.Errorf("%s: NeedsApproval disagrees", c.name)
		}
	}
	for _, code := range []string{tools.ReasonSetToAsk, tools.ReasonToolRule, tools.ReasonDestructive, tools.ReasonUnattended, tools.ReasonUntrusted, tools.ReasonOthersWrote, tools.ReasonSendsFile} {
		if tools.ReasonText(code) == tools.ReasonText("unknown") {
			t.Errorf("no text for %s", code)
		}
	}
}
