package eval_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/reminders/eval"
)

// answering is a scripted model that answers every phrase with fields from
// answer (no live calls).
func answering(answer func(p eval.Phrase) map[string]any) *models.ScriptedModel {
	return models.NewScripted().WithFallback(models.Step{Fn: func(_ context.Context, o models.CallOptions) ([]models.Event, error) {
		text, _ := o.Prompt[len(o.Prompt)-1].Text()
		for _, p := range eval.Phrases {
			if p.Text == text {
				return models.Say(jsonx.Stringify(answer(p))).Events, nil
			}
		}
		return models.Say("not json").Events, nil
	}})
}

func expected(p eval.Phrase) map[string]any {
	var rr any
	if p.RRule != "" {
		rr = p.RRule
	}
	return map[string]any{"text": p.Text, "localDateTime": p.LocalDateTime, "rrule": rr}
}

func TestPhrases(t *testing.T) {
	id, en := 0, 0
	for _, p := range eval.Phrases {
		if p.Lang == "id" {
			id++
		} else {
			en++
		}
	}
	if id != 30 || en != 30 {
		t.Fatal(id, en)
	}
}

func TestRunScoresAPerfectModel(t *testing.T) {
	var out bytes.Buffer
	code := eval.Run(context.Background(), answering(expected), "mock", "", reminders.ParseOptions{}, &out)
	if code != 0 || !strings.Contains(out.String(), "Indonesian 30/30, English 30/30\nScore: 60/60 (100.0%), pass mark 95%,") {
		t.Fatalf("%d\n%s", code, out.String())
	}
	if !strings.HasPrefix(out.String(), "Reminder parse eval: model mock, now 2026-11-10T10:00 Asia/Makassar (Tuesday), 60 phrases\n\n") {
		t.Fatal(out.String())
	}
}

func TestRunReportsFailures(t *testing.T) {
	var out bytes.Buffer
	wrong := func(p eval.Phrase) map[string]any {
		e := expected(p)
		if strings.Contains(p.Text, "listrik") {
			e["localDateTime"] = "2026-11-12T09:00"
		}
		if strings.Contains(p.Text, "olahraga") {
			e["rrule"] = "FREQ=WEEKLY"
		}
		return e
	}
	code := eval.Run(context.Background(), answering(wrong), "mock", "", reminders.ParseOptions{}, &out)
	s := out.String()
	if code != 0 { // 58/60 passes
		t.Fatalf("%d %s", code, s)
	}
	for _, want := range []string{
		"FAIL [id] ingetin bayar listrik besok jam 9\n  expected 2026-11-11T09:00 one-off\n  got      2026-11-12T09:00 one-off (time)",
		"FAIL [id] setiap hari jam 7 pagi olahraga\n  expected 2026-11-11T07:00 FREQ=DAILY\n  got      2026-11-11T07:00 FREQ=WEEKLY (rrule)",
		"Indonesian 28/30, English 30/30\nScore: 58/60 (96.7%), pass mark 95%",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	out.Reset()
	if code := eval.Run(context.Background(), answering(wrong), "mock", "listrik", reminders.ParseOptions{}, &out); code != 1 || !strings.Contains(out.String(), "Score: 0/1 (0.0%)") {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestMatchesAnEquivalentRule(t *testing.T) {
	p := eval.Phrase{Lang: "en", Text: "x", LocalDateTime: "2026-11-11T08:30", RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"}
	now, _ := reminders.LocalToUTC(eval.EvalNowLocal, eval.EvalTZ)
	at, _ := reminders.LocalToUTC("2026-11-11T08:30", eval.EvalTZ)
	same := "FREQ=DAILY;BYDAY=MO,TU,WE,TH,FR"
	if ok, why := eval.Matches(p, reminders.ParsedReminder{OK: true, FireAt: &at, RRule: &same}, now); !ok {
		t.Fatal(why)
	}
	other := "FREQ=DAILY"
	if ok, why := eval.Matches(p, reminders.ParsedReminder{OK: true, FireAt: &at, RRule: &other}, now); ok || why != "rrule" {
		t.Fatal(why)
	}
	bad := "FREQ=NOPE"
	if ok, _ := eval.Matches(p, reminders.ParsedReminder{OK: true, FireAt: &at, RRule: &bad}, now); ok {
		t.Fatal("matched a broken rule")
	}
}

func TestLoadConfig(t *testing.T) {
	env := map[string]string{"PALUI_EVAL_KEY": " k \n"}
	c, err := eval.LoadConfig(func(k string) string { return env[k] })
	if err != nil || c.Key != "k" || c.Base != "http://127.0.0.1:8317" || c.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("%+v %v", c, err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := eval.LoadConfig(func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "cliproxy-key") {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "palui")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cliproxy-key"), []byte("filekey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := eval.LoadConfig(func(string) string { return "" }); err != nil || c.Key != "filekey" {
		t.Fatalf("%+v %v", c, err)
	}
}
