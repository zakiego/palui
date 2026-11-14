package flows_test

// validate.test.ts

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

var vctx = flows.ValidateContext{
	ResolveTool: flows.ResolverFrom(ExampleTools),
	HookExists:  func(id string) bool { return id == "hk_github_palui" },
	ChatID:      "chat_owner",
	ChatAllowed: func(id string) string {
		switch id {
		case "chat_owner", "chat_web":
			return "own"
		case "chat_family":
			return "allowed"
		}
		return ""
	},
}

// base: a definition with the steps and a manual trigger at the first step.
func base(steps string, triggers ...string) any {
	trig := ""
	if len(triggers) > 0 {
		trig = triggers[0]
	} else {
		first := js(steps).([]any)[0].(*jsonx.Object)
		id, _ := first.Get("id")
		trig = fmt.Sprintf(`[{"id":"main","kind":"manual","start":%q}]`, id)
	}
	return js(fmt.Sprintf(`{"name":"T","triggers":%s,"steps":%s}`, trig, steps))
}

func problems(def any) []string {
	out := []string{}
	for _, p := range flows.ValidateDefinition(def, vctx).Problems {
		out = append(out, p.Path+": "+p.Message)
	}
	return out
}

func first(t *testing.T, list []string) string {
	t.Helper()
	if len(list) == 0 {
		t.Fatalf("no problems")
	}
	return list[0]
}

func match(t *testing.T, s, re string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(s) {
		t.Errorf("%q doesn't match /%s/", s, re)
	}
}

func TestValidateWorkedExamples(t *testing.T) {
	for _, ex := range Examples {
		t.Run(ex.Name+" validates with no write calls", func(t *testing.T) {
			r := flows.ValidateDefinition(js(ex.JSON), vctx)
			eq(t, r.Problems, []flows.Problem{}, "problems")
			if !r.OK || len(r.WriteCalls) != 0 {
				t.Errorf("ok %v, write calls %v", r.OK, r.WriteCalls)
			}
		})
	}
	t.Run("fills defaults and records the resolved tools", func(t *testing.T) {
		r := flows.ValidateDefinition(js(BRIEFING), vctx)
		if r.Definition.Schema != 1 {
			t.Errorf("schema %d", r.Definition.Schema)
		}
		w := r.Tools["weather"]
		if w.ConnectorID != "webfetch" || w.Tool != "fetch_page" || w.Class != "read" || !w.Untrusted {
			t.Errorf("weather %+v", w)
		}
	})
}

func TestValidateGraph(t *testing.T) {
	t.Run("rejects unknown targets, duplicate ids and reserved ids", func(t *testing.T) {
		eq(t, problems(base(`[{"id":"a","kind":"message","text":"x","next":"nope"}]`)), []string{`steps[0].next: Step "a" points next to "nope", which isn't a step.`}, "unknown target")
		match(t, first(t, problems(base(`[{"id":"a","kind":"message","text":"x"},{"id":"a","kind":"message","text":"y"}]`))), "used twice")
		match(t, first(t, problems(base(`[{"id":"fail","kind":"message","text":"x"}]`))), "reserved")
		match(t, first(t, problems(base(`[{"id":"a","kind":"message","text":"x"}]`, `[{"id":"t","kind":"manual","start":"zz"}]`))), "isn't a step")
	})
	t.Run("rejects cycles and unreachable steps", func(t *testing.T) {
		match(t, first(t, problems(base(`[{"id":"a","kind":"message","text":"x","next":"b"},{"id":"b","kind":"if","when":"true","then":"a"}]`))), "loop: a -> b -> a")
		eq(t, problems(base(`[{"id":"a","kind":"message","text":"x","next":null},{"id":"b","kind":"message","text":"y"}]`)), []string{`steps[1]: Step "b" can't be reached from any trigger. Point a next, branch or trigger start at it, or remove it.`}, "orphan")
	})
	t.Run("an if uses then/else, and on.<answer> must close", func(t *testing.T) {
		match(t, first(t, problems(base(`[{"id":"a","kind":"if","when":"true","then":null,"next":null}]`))), "use then/else")
		match(t, first(t, problems(base(`[{"id":"a","kind":"checkin","text":"?","deadline":{"kind":"after","minutes":60},"on":{"not_yet":"b"}},{"id":"b","kind":"message","text":"x"}]`))), "doesn't close")
	})
	t.Run("rejects nested each and item outside a body", func(t *testing.T) {
		nested := base(`[{"id":"t","kind":"tool","tool":"mcp-keuangan__list_bills"},{"id":"outer","kind":"each","items":"steps.t.data","body":"inner","next":null},{"id":"inner","kind":"each","items":"steps.t.data","body":"m","next":null},{"id":"m","kind":"message","text":"{{item}}"}]`)
		match(t, strings.Join(problems(nested), "\n"), "can't run inside another each")
		match(t, first(t, problems(base(`[{"id":"m","kind":"message","text":"{{item.name}}"}]`))), `reads item/index but isn't only inside an each body`)
	})
}

func TestValidateExpressions(t *testing.T) {
	t.Run("reports parse errors with the path", func(t *testing.T) {
		match(t, first(t, problems(base(`[{"id":"a","kind":"if","when":"steps.x = 1","then":null}]`))), `^steps\[0\]\.when: Use "=="`)
		match(t, first(t, problems(base(`[{"id":"a","kind":"message","text":"{{steps.x | nope}}"}]`))), `^steps\[0\]\.text: Unknown filter`)
		match(t, first(t, problems(base(`[{"id":"a","kind":"set","values":{"n":"flow.n +"}}]`))), `^steps\[0\]\.values\.n:`)
	})
	t.Run("keeps templates out of literal places", func(t *testing.T) {
		match(t, first(t, problems(base(`[{"id":"a","kind":"prompt","prompt":"Summarize {{trigger.payload}}"}]`))), "prompt instruction is a literal")
		match(t, first(t, problems(base(`[{"id":"a","kind":"message","text":"x","to":"{{trigger.chat}}"}]`))), "message.to is a literal")
		match(t, first(t, problems(base(`[{"id":"a","kind":"tool","tool":"reminders__list_reminders","args":{"{{trigger.k}}":1}}]`))), "Arg names are literals")
		// Ids, kinds and targets are literals by shape.
		found := false
		for _, p := range problems(base(`[{"id":"{{trigger.x}}","kind":"message","text":"x"}]`, `[{"id":"main","kind":"manual","start":"x"}]`)) {
			if strings.HasPrefix(p, "steps[0].id:") {
				found = true
			}
		}
		if !found {
			t.Error("no steps[0].id problem")
		}
	})
	t.Run("checks wait, chat commands, events and deadline next", func(t *testing.T) {
		match(t, first(t, problems(base(`[{"id":"w","kind":"wait"}]`))), "exactly one of minutes or until")
		match(t, first(t, problems(base(`[{"id":"m","kind":"message","text":"x"}]`, `[{"id":"c","kind":"chat","start":"m","command":"/status"}]`))), "built-in command")
		for _, cmd := range []string{"/help", "/usage", "/btw", "/id", "/start"} {
			match(t, first(t, problems(base(`[{"id":"m","kind":"message","text":"x"}]`, fmt.Sprintf(`[{"id":"c","kind":"chat","start":"m","command":%q}]`, cmd)))), "built-in command")
		}
		match(t, first(t, problems(base(`[{"id":"m","kind":"message","text":"x"}]`, `[{"id":"e","kind":"event","start":"m","event":"run.failed"}]`))), "own run.failed")
		match(t, first(t, problems(base(`[{"id":"a","kind":"checkin","text":"?"}]`))), `deadline "next"`)
	})
	t.Run("checks computed check-in deadlines", func(t *testing.T) {
		ck := func(deadline string) any {
			return base(fmt.Sprintf(`[{"id":"a","kind":"checkin","text":"?","deadline":%s}]`, deadline))
		}
		eq(t, problems(ck(`{"kind":"at","at":"{{trigger.args.next}}","plusMin":-10}`)), []string{}, "at template")
		eq(t, problems(ck(`{"kind":"at","at":"2026-11-12T19:29"}`)), []string{}, "at iso")
		eq(t, problems(ck(`{"kind":"after","minutes":"{{trigger.args.left}}"}`)), []string{}, "after template")
		match(t, first(t, problems(ck(`{"kind":"at","at":"besok"}`))), "deadline.at is a template or an ISO")
		match(t, first(t, problems(ck(`{"kind":"after","minutes":"sejam"}`))), "deadline.minutes is a number or a template")
		found := false
		for _, p := range problems(ck(`{"kind":"at","at":"{{vars.}}"}`)) {
			if strings.HasPrefix(p, "steps[0].deadline.at:") {
				found = true
			}
		}
		if !found {
			t.Error("no deadline.at problem")
		}
	})
}

func TestValidateTools(t *testing.T) {
	t.Run("refuses destructive tools and unknown tools", func(t *testing.T) {
		match(t, first(t, problems(base(`[{"id":"a","kind":"tool","tool":"reminders__cancel_reminder"}]`))), "destructive. Flows never run destructive tools")
		match(t, first(t, problems(base(`[{"id":"a","kind":"tool","tool":"nope__x"}]`))), "No enabled tool nope__x")
		match(t, first(t, problems(base(`[{"id":"a","kind":"prompt","prompt":"x","tools":["reminders__cancel_reminder"]}]`))), "never get destructive")
	})
	t.Run("resolves connectorId + tool and lists write calls with templates as written", func(t *testing.T) {
		r := flows.ValidateDefinition(base(`[{"id":"a","kind":"tool","connectorId":"mcp-keuangan","tool":"mark_paid","args":{"id":"{{trigger.id}}"}}]`), vctx)
		if !r.OK {
			t.Fatalf("problems %v", r.Problems)
		}
		eq(t, r.WriteCalls, js(`[{"stepId":"a","kind":"tool","connectorId":"mcp-keuangan","connectorType":"mcp","tool":"mark_paid","class":"write","args":{"id":"{{trigger.id}}"}}]`), "write calls")
		eq(t, flows.DescribeWriteCall(r.WriteCalls[0]), "mcp-keuangan.mark_paid(id: {{trigger.id}})", "describe")
	})
	t.Run("counts message.to an allowed chat as a write call, the owner's own chats as not", func(t *testing.T) {
		eq(t, flows.ValidateDefinition(base(`[{"id":"a","kind":"message","text":"x","to":"chat_web"}]`), vctx).WriteCalls, js(`[]`), "own")
		eq(t, flows.ValidateDefinition(base(`[{"id":"a","kind":"message","text":"x","to":"chat_family"}]`), vctx).WriteCalls, js(`[{"stepId":"a","kind":"message","to":"chat_family"}]`), "family")
		match(t, first(t, problems(base(`[{"id":"a","kind":"message","text":"x","to":"chat_stranger"}]`))), "isn't one of the owner's chats")
	})
	t.Run("sameWriteCalls ignores order and spots changed args", func(t *testing.T) {
		a := []flows.WriteCall{{StepID: "x", Kind: "tool", ConnectorID: "c", Tool: "t", Class: "write", Args: jobj(`{"a":1,"b":"{{x}}"}`)}}
		b := []flows.WriteCall{{StepID: "renamed", Kind: "tool", ConnectorID: "c", Tool: "t", Class: "write", Args: jobj(`{"b":"{{x}}","a":1}`)}}
		c := []flows.WriteCall{{StepID: "x", Kind: "tool", ConnectorID: "c", Tool: "t", Class: "write", Args: jobj(`{"a":2,"b":"{{x}}"}`)}}
		if !flows.SameWriteCalls(a, b) || flows.SameWriteCalls(a, c) {
			t.Error("sameWriteCalls")
		}
	})
}

func TestValidateLimits(t *testing.T) {
	t.Run("caps steps and size", func(t *testing.T) {
		var steps []string
		for i := 0; i < 41; i++ {
			steps = append(steps, fmt.Sprintf(`{"id":"s%d","kind":"message","text":"x"}`, i))
		}
		match(t, first(t, problems(base("["+strings.Join(steps, ",")+"]"))), "^steps:")
		big := []string{fmt.Sprintf(`{"id":"a","kind":"message","text":%q}`, strings.Repeat("x", 4000))}
		for i := 0; i < 9; i++ {
			big = append(big, fmt.Sprintf(`{"id":"b%d","kind":"message","text":%q}`, i, strings.Repeat("y", 4000)))
		}
		match(t, first(t, problems(base("["+strings.Join(big, ",")+"]"))), "limit is 32 KB")
	})
	t.Run("hashes canonically", func(t *testing.T) {
		if flows.CanonicalJSON(js(`{"b":1,"a":{"d":1,"c":2}}`)) != flows.CanonicalJSON(js(`{"a":{"c":2,"d":1},"b":1}`)) {
			t.Error("canonical JSON differs")
		}
	})
}

func TestValidateNamingProblems(t *testing.T) {
	t.Run("names the bad step id and set key instead of a bare zod message", func(t *testing.T) {
		def := base(`[{"id":"start","kind":"if","when":"true","then":"Init","else":null},{"id":"Init","kind":"set","values":{"L":"now"},"next":null}]`)
		eq(t, problems(def), []string{
			`steps[0].then: "Init": Ids use lowercase letters, digits and _, starting with a letter (max 32), like ask_1.`,
			`steps[1].id: "Init": Ids use lowercase letters, digits and _, starting with a letter (max 32), like ask_1.`,
			`steps[1].values.L: Key "L": Variable names use lowercase letters, digits and _, starting with a letter (max 32), like last_at.`,
		}, "problems")
	})
	t.Run("accepts a templated after deadline and a computed at deadline", func(t *testing.T) {
		def := base(`[{"id":"calc","kind":"set","values":{"left":"10"}},{"id":"ask","kind":"checkin","text":"x","deadline":{"kind":"after","minutes":"{{vars.left}}"}},{"id":"ask2","kind":"checkin","text":"y","deadline":{"kind":"at","at":"{{vars.left}}","plusMin":-10},"next":null}]`)
		eq(t, problems(def), []string{}, "problems")
	})
}
