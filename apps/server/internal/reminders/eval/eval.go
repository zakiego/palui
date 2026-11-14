// Package eval is the live reminder parse eval (reminders/eval/run.ts, PLAN
// 6, 17): every phrase in Phrases through the parser against a fixed "now",
// scored on exact matches. It makes live model calls, so it never runs in the
// gate: `palui eval-reminders` runs it (see Config for its settings).
package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/reminders"
)

const (
	PassRate    = 0.95
	Concurrency = 4
)

// Config comes from PALUI_EVAL_BASE (default http://127.0.0.1:8317),
// PALUI_EVAL_KEY (default: the contents of ~/.config/palui/cliproxy-key),
// PALUI_EVAL_MODEL (default claude-haiku-4-5-20251001) and PALUI_EVAL_ONLY
// (substring filter on the phrase).
type Config struct {
	Base  string
	Key   string
	Model string
	Only  string
}

// LoadConfig reads the PALUI_EVAL_* variables and the key file.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{Base: getenv("PALUI_EVAL_BASE"), Key: strings.TrimSpace(getenv("PALUI_EVAL_KEY")), Model: getenv("PALUI_EVAL_MODEL"), Only: getenv("PALUI_EVAL_ONLY")}
	if c.Base == "" {
		c.Base = "http://127.0.0.1:8317"
	}
	if c.Model == "" {
		c.Model = "claude-haiku-4-5-20251001"
	}
	if c.Key == "" {
		home, _ := os.UserHomeDir()
		file := filepath.Join(home, ".config", "palui", "cliproxy-key")
		b, err := os.ReadFile(file)
		if err != nil {
			return c, fmt.Errorf("Set PALUI_EVAL_KEY or put the CLIProxyAPI key in %s.", file)
		}
		c.Key = strings.TrimSpace(string(b))
	}
	return c, nil
}

func safeNormalize(rule string) string {
	if rule == "" {
		return ""
	}
	n, err := reminders.NormalizeRrule(rule)
	if err != nil {
		return "INVALID(" + rule + ")"
	}
	return n
}

// Matches: the same fire instant, and the same rule (equal normalized bodies
// or, for rules written differently, the same next 8 occurrences). why is
// "time" or "rrule" when it doesn't match.
func Matches(p Phrase, got reminders.ParsedReminder, now int64) (bool, string) {
	expectedAt, err := reminders.LocalToUTC(p.LocalDateTime, EvalTZ)
	if err != nil || got.FireAt == nil || *got.FireAt != expectedAt {
		return false, "time"
	}
	a := safeNormalize(p.RRule)
	gr := ""
	if got.RRule != nil {
		gr = *got.RRule
	}
	b := safeNormalize(gr)
	if a == b {
		return true, ""
	}
	if a == "" || b == "" || strings.HasPrefix(b, "INVALID") {
		return false, "rrule"
	}
	ea, err1 := reminders.OccurrencesAfter(reminders.Schedule{RRule: &a, TZ: EvalTZ, LocalStart: p.LocalDateTime}, now, 8)
	eb, err2 := reminders.OccurrencesAfter(reminders.Schedule{RRule: &b, TZ: EvalTZ, LocalStart: reminders.UTCToLocal(*got.FireAt, EvalTZ)}, now, 8)
	if err1 == nil && err2 == nil && len(ea) == len(eb) {
		same := true
		for i := range ea {
			if ea[i] != eb[i] {
				same = false
			}
		}
		if same {
			return true, ""
		}
	}
	return false, "rrule"
}

type result struct {
	p   Phrase
	got reminders.ParsedReminder
	ok  bool
	why string
}

// Run scores phrases (filtered by only) with the model, writes the report and
// returns the exit code: 0 at or above PassRate, 1 below.
func Run(ctx context.Context, model models.LanguageModel, modelID, only string, opts reminders.ParseOptions, out io.Writer) int {
	now, err := reminders.LocalToUTC(EvalNowLocal, EvalTZ)
	if err != nil {
		fmt.Fprintf(out, "eval crashed: %s\n", err)
		return 2
	}
	var phrases []Phrase
	for _, p := range Phrases {
		if only == "" || strings.Contains(p.Text, only) {
			phrases = append(phrases, p)
		}
	}
	started := time.Now()
	results := make([]result, len(phrases))
	var next int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < min(Concurrency, len(phrases)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= len(phrases) {
					return
				}
				p := phrases[i]
				got := reminders.ParseReminder(ctx, p.Text, EvalTZ, now, model, opts)
				r := result{p: p, got: got}
				if got.OK {
					r.ok, r.why = Matches(p, got, now)
				} else {
					r.why = "error: " + got.Error
				}
				results[i] = r
			}
		}()
	}
	wg.Wait()

	var lines []string
	lines = append(lines, fmt.Sprintf("Reminder parse eval: model %s, now %s %s (Tuesday), %d phrases", modelID, EvalNowLocal, EvalTZ, len(phrases)), "")
	passed := 0
	failures := 0
	for _, r := range results {
		if r.ok {
			passed++
			continue
		}
		failures++
		gotLocal := "-"
		if r.got.FireAt != nil {
			gotLocal = reminders.UTCToLocal(*r.got.FireAt, EvalTZ)[:16]
		}
		gotRule := ""
		if r.got.RRule != nil {
			gotRule = *r.got.RRule
		} else if r.got.OK {
			gotRule = "one-off"
		}
		want := p(r.p.RRule)
		lines = append(lines, fmt.Sprintf("FAIL [%s] %s", r.p.Lang, r.p.Text), fmt.Sprintf("  expected %s %s", r.p.LocalDateTime, want), fmt.Sprintf("  got      %s %s (%s)", gotLocal, gotRule, r.why))
	}
	byLang := func(lang string) string {
		n, ok := 0, 0
		for _, r := range results {
			if r.p.Lang == lang {
				n++
				if r.ok {
					ok++
				}
			}
		}
		return fmt.Sprintf("%d/%d", ok, n)
	}
	if failures > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, fmt.Sprintf("Indonesian %s, English %s", byLang("id"), byLang("en")))
	rate := 0.0
	if len(results) > 0 {
		rate = float64(passed) / float64(len(results))
	}
	lines = append(lines, fmt.Sprintf("Score: %d/%d (%.1f%%), pass mark %g%%, %.1f s", passed, len(results), rate*100, PassRate*100, time.Since(started).Seconds()))
	fmt.Fprintln(out, strings.Join(lines, "\n"))
	if rate >= PassRate {
		return 0
	}
	return 1
}

func p(rule string) string {
	if rule == "" {
		return "one-off"
	}
	return rule
}

// Main is `palui eval-reminders`: the live run against CLIProxyAPI's
// Anthropic endpoint (exit 0 pass, 1 below the pass mark, 2 crashed).
func Main(ctx context.Context, getenv func(string) string, out, errOut io.Writer) int {
	cfg, err := LoadConfig(getenv)
	if err != nil {
		fmt.Fprintf(errOut, "eval crashed: %s\n", err)
		return 2
	}
	if cfg.Key == "" {
		fmt.Fprintln(errOut, "eval crashed: "+errors.New("empty API key").Error())
		return 2
	}
	two := 2
	model := models.NewAnthropic(models.AnthropicConfig{BaseURL: cfg.Base + "/v1", APIKey: cfg.Key}, cfg.Model)
	return Run(ctx, model, cfg.Model, cfg.Only, reminders.ParseOptions{Timeout: 30 * time.Second, MaxRetries: &two}, out)
}
