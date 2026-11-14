package flows_test

// expr.test.ts

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/flows"
)

var exprNow = at("2026-11-11T09:00")

func env(scope string) flows.EvalEnv {
	s := flows.Scope{}
	if scope != "" {
		o := jobj(scope)
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			s[k] = v
		}
	}
	return flows.EvalEnv{Scope: s, TZ: WITA, Now: exprNow}
}

func runExpr(t *testing.T, src, scope string) any {
	t.Helper()
	ast, err := flows.ParseExpr(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return flows.Evaluate(ast, env(scope)).Value
}

func parseFails(t *testing.T, src, re string) {
	t.Helper()
	_, err := flows.ParseExpr(src)
	if err == nil {
		t.Fatalf("%q parsed", src)
	}
	var ee *flows.ExprError
	if !errors.As(err, &ee) {
		t.Fatalf("%q: not an ExprError: %v", src, err)
	}
	if re != "" {
		match(t, err.Error(), re)
	}
}

func TestExprParser(t *testing.T) {
	t.Run("parses precedence", func(t *testing.T) {
		eq(t, runExpr(t, "1 + 2 * 3", ""), 7.0, "1+2*3")
		eq(t, runExpr(t, "(1 + 2) * 3", ""), 9.0, "(1+2)*3")
		eq(t, runExpr(t, "not 1 == 2 and 3 > 2", ""), true, "not")
		eq(t, runExpr(t, "false or true and false", ""), false, "or/and")
		eq(t, runExpr(t, "-2 * -3", ""), 6.0, "neg")
	})
	t.Run("reads paths with dots, numeric segments and brackets", func(t *testing.T) {
		scope := `{"steps":{"bills":{"data":{"items":[{"due":"2026-11-13","id":"a"}]}}},"flow":{"paid":{"a":true}},"item":{"id":"a"}}`
		eq(t, runExpr(t, "steps.bills.data.items.0.due", scope), "2026-11-13", "dots")
		eq(t, runExpr(t, `steps.bills.data.items[0]["due"]`, scope), "2026-11-13", "brackets")
		eq(t, runExpr(t, "flow.paid[item.id]", scope), true, "dynamic")
		eq(t, runExpr(t, "flow.paid[item.nope]", scope), nil, "dynamic missing")
	})
	t.Run("rejects unknown roots, functions, = and && with a sentence", func(t *testing.T) {
		parseFails(t, "foo.bar", "Paths start with")
		parseFails(t, "eval(1)", `Unknown function "eval"`)
		parseFails(t, "vars.a = 1", `"=="`)
		parseFails(t, "true && false", `"and"`)
		parseFails(t, "!true", `"not"`)
		parseFails(t, "1 < 2 < 3", "don't chain")
		parseFails(t, "len(1, 2)", "takes 1 argument")
		parseFails(t, `"open`, "closing quote")
		parseFails(t, "(1 + 2", "")
		parseFails(t, "1 2", "after the end")
		parseFails(t, "", "empty")
	})
	t.Run("caps the AST size at save time", func(t *testing.T) {
		parseFails(t, strings.TrimSuffix(strings.Repeat("1 + ", flows.MaxNodes), " + "), "more than 200 parts")
	})
	t.Run("parsePath only takes a plain path", func(t *testing.T) {
		p, err := flows.ParsePath("steps.x.data.items")
		if err != nil || p.Root != "steps" {
			t.Errorf("parsePath: %v %v", p, err)
		}
		_, err = flows.ParsePath("len(steps.x)")
		if err == nil || !strings.Contains(err.Error(), "plain path") {
			t.Errorf("parsePath(len): %v", err)
		}
	})
	t.Run("lists the paths read and their taint roots", func(t *testing.T) {
		ast, err := flows.ParseExpr(`daysUntil(item.due) < 3 and not flow.paid[item.id] or steps.ci.data.state == "x" or vars.n > 1`)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, p := range flows.PathsOf(ast, nil) {
			seen[flows.RootKey(p)] = true
		}
		var keys []string
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		eq(t, keys, []string{"flow.paid", "item", "steps.ci", "vars.n"}, "roots")
	})
}

func TestExprSemantics(t *testing.T) {
	t.Run("== is strict except numbers against numeric strings", func(t *testing.T) {
		eq(t, runExpr(t, `3 == "3"`, ""), true, `3 == "3"`)
		eq(t, runExpr(t, `"3" == "3.0"`, ""), false, `"3" == "3.0"`)
		eq(t, runExpr(t, `true == "true"`, ""), false, `true == "true"`)
		eq(t, runExpr(t, "null == null", ""), true, "null")
		eq(t, runExpr(t, "trigger.missing == null", ""), true, "missing == null")
		eq(t, runExpr(t, "trigger.missing != null", ""), false, "missing != null")
	})
	t.Run("ordering with null is false, and odd types warn and give false", func(t *testing.T) {
		eq(t, runExpr(t, "trigger.missing < 3", ""), false, "<")
		eq(t, runExpr(t, "trigger.missing >= 3", ""), false, ">=")
		ast, _ := flows.ParseExpr("trigger.list > 1")
		r := flows.Evaluate(ast, env(`{"trigger":{"list":[1,2]}}`))
		eq(t, r.Value, false, "value")
		match(t, r.Warnings[0], "can't compare a list")
	})
	t.Run("compares numeric strings as numbers and dates as text", func(t *testing.T) {
		eq(t, runExpr(t, `"10" > "9"`, ""), true, "numeric")
		eq(t, runExpr(t, `"2026-11-13" > "2026-11-12"`, ""), true, "dates")
	})
	t.Run("short-circuits and/or", func(t *testing.T) {
		eq(t, runExpr(t, "false and len(steps.x) > 0", ""), false, "and")
		eq(t, runExpr(t, "true or len(steps.x) > 0", ""), true, "or")
	})
	t.Run("division by zero is null; null counts as 0 in + and -", func(t *testing.T) {
		eq(t, runExpr(t, "1 / 0", ""), nil, "1/0")
		eq(t, runExpr(t, "flow.streak + 1", `{"flow":{}}`), 1.0, "null + 1")
		eq(t, runExpr(t, "flow.streak - 1", `{"flow":{"streak":3}}`), 2.0, "3 - 1")
		eq(t, runExpr(t, `"a" + "b"`, ""), "ab", "concat")
		eq(t, runExpr(t, `"3" + 1`, ""), 4.0, "numeric concat")
	})
	t.Run("runs every function", func(t *testing.T) {
		scope := `{"trigger":{"list":[{"s":"a"},{"s":"b"},{"s":"a"}],"text":"Halo Dunia"}}`
		cases := []struct {
			src  string
			want any
		}{
			{"exists(trigger.text)", true},
			{"empty(trigger.nope)", true},
			{`empty("")`, true},
			{`contains(trigger.text, "Dunia")`, true},
			{`contains(trigger.list, "a")`, false},
			{`startsWith(trigger.text, "Ha")`, true},
			{"len(trigger.list)", 3.0},
			{"lower(trigger.text)", "halo dunia"},
			{`any(trigger.list, "s", "b")`, true},
			{`count(trigger.list, "s", "a")`, 2.0},
			{`number("12.5")`, 12.5},
			{`number("x")`, nil},
			{"round(2.456, 2)", 2.46},
			{"min(3, 5)", 3.0},
			{"max(3, 5)", 5.0},
			{"choose(3 > 2, 'a', 'b')", "a"},
			{"choose(null, 1, 2)", 2.0},
			{`date("2026-11-11")`, float64(at("2026-11-11T00:00"))},
			{`daysUntil("2026-11-13")`, 2.0},
			{`daysUntil("2026-11-11T23:59")`, 0.0},
			{`hoursUntil("2026-11-11T12:00")`, 3.0},
			{`minutesSince("08:30")`, 30.0},
			{`weekday("2026-11-15")`, "SU"},
			{"weekday()", "WE"},
			{"today()", "2026-11-11"},
		}
		for _, c := range cases {
			eq(t, runExpr(t, c.src, scope), c.want, c.src)
		}
	})
	t.Run("logs the operands a condition compared", func(t *testing.T) {
		ast, _ := flows.ParseExpr("daysUntil(item.due) < 3")
		r, ok := flows.EvalCondition(ast, env(`{"item":{"due":"2026-11-13"}}`))
		if !ok {
			t.Error("result false")
		}
		eq(t, r.Trace, []flows.TraceEntry{{Expr: "item.due", Value: "2026-11-13"}, {Expr: "daysUntil(item.due)", Value: 2.0}}, "trace")
	})
	t.Run("evaluates a large expression within the step budget", func(t *testing.T) {
		src := strings.Repeat("len(trigger.list) + ", 60) + "0"
		eq(t, runExpr(t, src, `{"trigger":{"list":[0,1,2,3,4]}}`), 300.0, "len sum")
	})
	t.Run("never runs code from data", func(t *testing.T) {
		eq(t, runExpr(t, `trigger.payload.cmd == "x"`, `{"trigger":{"payload":{"cmd":"process.exit(1)"}}}`), false, "data")
	})
}
