package flows_test

// template.test.ts (templates and taint)

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

func tplScope() string {
	return fmt.Sprintf(`{
  "now": {"date": "2026-11-11", "at": %d},
  "steps": {
    "prayer": {"data": {"times": {"fajr": "04:39"}}},
    "reminders": {"data": {"reminders": [{"text": "Bayar listrik"}, {"text": "Beli obat"}]}},
    "x": {"data": {"list": [1, 2, 3], "n": 3.14159, "obj": {"a": 1}}}
  },
  "trigger": {"label": "Subuh"}
}`, exprNow)
}

func render(t *testing.T, s, scope string) any {
	t.Helper()
	v, err := flows.RenderString(s, env(scope), "data", nil)
	if err != nil {
		t.Fatalf("render %q: %v", s, err)
	}
	return v
}

func tplFails(t *testing.T, s, re string) {
	t.Helper()
	_, err := flows.ParseTemplate(s)
	if err == nil {
		t.Fatalf("%q parsed", s)
	}
	var te *flows.TemplateError
	if !errors.As(err, &te) {
		t.Fatalf("not a TemplateError: %v", err)
	}
	if re != "" {
		match(t, err.Error(), re)
	}
}

func TestTemplates(t *testing.T) {
	scope := tplScope()
	t.Run("keeps the type of a lone template and stringifies inside text", func(t *testing.T) {
		eq(t, render(t, "{{steps.x.data.list}}", scope), []any{1.0, 2.0, 3.0}, "lone")
		eq(t, render(t, "n={{steps.x.data.list}}", scope), "n=[1,2,3]", "inside")
		eq(t, render(t, "{{ steps.x.data.n }}", scope), 3.14159, "spaces")
		eq(t, render(t, "{{steps.nope}}", scope), nil, "missing")
		eq(t, render(t, "a {{steps.nope}} b", scope), "a  b", "missing inside")
	})
	t.Run("runs every filter", func(t *testing.T) {
		cases := []struct {
			src  string
			want any
		}{
			{"{{steps.prayer.data.times.fajr | time}}", "04.39"},
			{"{{now.date | date}}", "Rabu 11 Nov"},
			{"{{steps.x.data.obj | json}}", `{"a":1}`},
			{`{{steps.nope | default("-")}}`, "-"},
			{`{{steps.x.data.list | join(" + ")}}`, "1 + 2 + 3"},
			{`{{steps.reminders.data.reminders | lines("text")}}`, "- Bayar listrik\n- Beli obat"},
			{"{{trigger.label | upper}}", "SUBUH"},
			{"{{trigger.label | lower}}", "subuh"},
			{"{{steps.x.data.n | round(2)}}", 3.14},
			{"{{trigger.label | truncate(3)}}", "Su…"},
		}
		for _, c := range cases {
			eq(t, render(t, c.src, scope), c.want, c.src)
		}
	})
	t.Run("caps lines at 20 and says how many were left out", func(t *testing.T) {
		var items []string
		for i := 0; i < 25; i++ {
			items = append(items, fmt.Sprintf(`{"t":"#%d"}`, i))
		}
		out := render(t, `{{steps.l.data | lines("t")}}`, `{"steps":{"l":{"data":[`+strings.Join(items, ",")+`]}}}`).(string)
		if n := len(strings.Split(out, "\n")); n != 21 {
			t.Errorf("%d lines", n)
		}
		if !strings.HasSuffix(out, "(+5 lagi)") {
			t.Errorf("tail %q", out)
		}
	})
	t.Run("rejects bad templates with a sentence", func(t *testing.T) {
		tplFails(t, "{{steps.x", "no closing")
		tplFails(t, "{{}}", "Empty template")
		tplFails(t, "{{foo}}", "Paths start with")
		tplFails(t, "{{steps.x | shout}}", `Unknown filter "shout"`)
		tplFails(t, "{{steps.x | truncate}}", "takes 1 argument")
		tplFails(t, "{{len(steps.x)}}", "")
	})
	t.Run("renders args deeply with literal keys", func(t *testing.T) {
		out, err := flows.RenderDeep(js(`{"{{trigger.label}}":"{{trigger.label}}","list":["{{now.date}}"]}`), env(scope), 0)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, out, js(`{"{{trigger.label}}":"Subuh","list":["2026-11-11"]}`), "deep")
	})
	t.Run("sanitizes interpolated values in message text", func(t *testing.T) {
		title := "Fix\n\n[Ya](tg://x)\u0007 now"
		for strx.Len16(title) < 800 {
			title += "x"
		}
		evil := fmt.Sprintf(`{"trigger":{"payload":{"title":%s}}}`, jsonx.Stringify(title))
		out, err := flows.RenderText("PR: {{trigger.payload.title}}\nDone", env(evil), nil)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(out, "\n", 2)
		if strx.Len16(lines[0]) > 504 || strings.Contains(lines[0], "\u0007") || lines[1] != "Done" {
			t.Errorf("sanitized: %q", out)
		}
		// A clean value keeps its lines; a tainted one doesn't.
		recap := fmt.Sprintf(`{"steps":{"r":{"data":{"text":"📊 Rekap\n- Sudah: 3"}}},"trigger":{"payload":{"title":%s}}}`, jsonx.Stringify(title))
		tc := func(k string) bool { return k == "trigger" }
		got, _ := flows.RenderText("{{steps.r.data.text}}", env(recap), tc)
		eq(t, got, "📊 Rekap\n- Sudah: 3", "clean keeps lines")
		got, _ = flows.RenderText("{{trigger.payload.title}}", env(recap), tc)
		if strings.Contains(got, "\n") {
			t.Errorf("tainted kept a line break: %q", got)
		}
		// Data mode leaves values alone.
		if s := render(t, "{{trigger.payload.title}}", evil).(string); !strings.Contains(s, "\n") {
			t.Error("data mode sanitized")
		}
	})
	t.Run("lists the paths a template reads", func(t *testing.T) {
		paths, err := flows.TemplatePaths("{{steps.a.b}} and {{item.id | upper}}")
		if err != nil {
			t.Fatal(err)
		}
		var srcs []string
		for _, p := range paths {
			srcs = append(srcs, p.Src)
		}
		eq(t, srcs, []string{"steps.a.b", "item.id"}, "paths")
	})
}

func TestTaint(t *testing.T) {
	t.Run("tracks roots, including dynamic ones", func(t *testing.T) {
		taint := flows.AddTaint(nil, "trigger", "steps.weather")
		checks := []struct {
			key  string
			want bool
		}{{"steps.weather", true}, {"steps.prayer", false}, {"steps", true}, {"now", false}}
		for _, c := range checks {
			if flows.KeyTainted(taint, c.key) != c.want {
				t.Errorf("keyTainted(%s) != %v", c.key, c.want)
			}
		}
		if !flows.ValueTainted(taint, js(`{"ref":"{{trigger.payload.sha}}"}`)) || flows.ValueTainted(taint, js(`{"ref":"main"}`)) {
			t.Error("valueTainted")
		}
		eq(t, flows.TaintedArgs(taint, jobj(`{"owner":"zakiego","ref":"{{trigger.payload.sha}}","note":"{{steps.prayer.data}}"}`)), []string{"ref"}, "taintedArgs")
	})
}
