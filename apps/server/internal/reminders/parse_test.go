package reminders_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

func reply(v any) models.Step { return models.Say(jsonx.Stringify(v)) }

func parse(m models.LanguageModel, text, tz string, retries *int) reminders.ParsedReminder {
	return reminders.ParseReminder(context.Background(), text, tz, NOW, m, reminders.ParseOptions{MaxRetries: retries, RetryDelay: time.Millisecond})
}

func TestParseReminder(t *testing.T) {
	t.Run("returns the instant, ISO with offset and the echo", func(t *testing.T) {
		m := models.NewScripted(reply(map[string]any{"text": "bayar listrik", "localDateTime": "2026-11-11T09:00", "rrule": nil}))
		r := parse(m, "ingetin bayar listrik besok jam 9", WITA, nil)
		want := `{"ok":true,"text":"bayar listrik","fireAt":` + jsonx.Stringify(l2u(t, "2026-11-11T09:00", WITA)) + `,"fireAtIso":"2026-11-11T09:00:00.000+08:00","rrule":null,"tz":"Asia/Makassar","echo":"Besok, Rabu 11 Nov, 09.00 WITA"}`
		if got := jsonx.Stringify(r); got != want {
			t.Fatalf("%s\n%s", got, want)
		}
		if reminders.ParsedLocalStart(r) != "2026-11-11T09:00:00" {
			t.Fatal(reminders.ParsedLocalStart(r))
		}
		// The system prompt carries now, the weekday and the zone.
		if !strings.Contains(models.PromptText(m.Prompts[0]), "2026-11-10T10:00 (Tuesday / Selasa), zone Asia/Makassar (WITA, UTC+08:00)") {
			t.Fatal("prompt")
		}
		c := m.Calls[0]
		if *c.Temperature != 0 || *c.MaxOutputTokens != 400 || c.ResponseFormat == nil || c.ResponseFormat.Type != "json" || string(c.ResponseFormat.Schema) != reminders.ParsedReminderSchema {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("normalizes a recurring rule and describes it", func(t *testing.T) {
		m := models.NewScripted(reply(map[string]any{"text": "kirim laporan", "localDateTime": "2026-11-16T08:00", "rrule": "BYDAY=MO;FREQ=WEEKLY"}))
		r := parse(m, "tiap senin jam 8 kirim laporan", WITA, nil)
		if !r.OK || *r.RRule != "FREQ=WEEKLY;BYDAY=MO" || r.Echo != "Tiap Senin, 08.00 WITA, mulai Senin 16 Nov" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("never fails on model errors", func(t *testing.T) {
		zero := 0
		m := models.NewScripted(models.Fail(errors.New("503 upstream")))
		r := parse(m, "besok jam 9", WITA, &zero)
		if r.OK || !strings.Contains(r.Error, "503 upstream") || r.Text != "besok jam 9" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("retries a retryable API error once", func(t *testing.T) {
		e503 := &models.APICallError{Message: "HTTP 503", StatusCode: 503, IsRetryable: true}
		m := models.NewScripted(models.Fail(e503), reply(map[string]any{"text": "x", "localDateTime": "2026-11-11T09:00", "rrule": nil}))
		if r := parse(m, "besok", WITA, nil); !r.OK {
			t.Fatalf("%+v", r)
		}
		failing := models.NewScripted(models.Fail(e503), models.Fail(e503))
		if r := parse(failing, "besok", WITA, nil); r.Error != "Couldn't read that reminder: Failed after 2 attempts. Last error: HTTP 503" {
			t.Fatal(r.Error)
		}
	})
	t.Run("reports unparseable model output", func(t *testing.T) {
		zero := 0
		for in, want := range map[string]string{
			"not json at all": "Couldn't read that reminder: No object generated: could not parse the response.",
			`{"text":"x"}`:    "Couldn't read that reminder: No object generated: response did not match schema.",
			`[1]`:             "Couldn't read that reminder: No object generated: response did not match schema.",
		} {
			r := parse(models.NewScripted(models.Say(in)), "besok", WITA, &zero)
			if r.OK || r.Error != want {
				t.Fatalf("%s: %+v", in, r)
			}
		}
		empty := models.NewScripted(models.Step{Events: []models.Event{{Type: models.EventFinish, FinishReason: models.FinishReason{Unified: "length"}}}})
		if r := parse(empty, "besok", WITA, &zero); r.Error != "Couldn't read that reminder: No output generated." {
			t.Fatal(r.Error)
		}
	})
	t.Run("reads the Anthropic JSON tool fallback", func(t *testing.T) {
		m := models.NewScripted(models.CallTool(models.JSONToolName, map[string]any{"text": "x", "localDateTime": "2026-11-11T09:00", "rrule": nil}, "toolu_1"))
		if r := parse(m, "besok", WITA, nil); !r.OK {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("times out", func(t *testing.T) {
		m := models.NewScripted(models.Step{Fn: func(ctx context.Context, _ models.CallOptions) ([]models.Event, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}})
		r := reminders.ParseReminder(context.Background(), "besok", WITA, NOW, m, reminders.ParseOptions{Timeout: time.Millisecond})
		if r.Error != "Couldn't read that reminder: The operation was aborted due to timeout" {
			t.Fatal(r.Error)
		}
	})
	t.Run("refuses an empty phrase and a bad zone without calling the model", func(t *testing.T) {
		m := models.NewScripted()
		if parse(m, "  ", WITA, nil).OK || parse(m, "besok", "Bad/Zone", nil).OK || len(m.Calls) != 0 {
			t.Fatal("called")
		}
	})
}

func finish(t *testing.T, text, local string, rr *string, fallback string) reminders.ParsedReminder {
	t.Helper()
	r, err := reminders.FinishParse(reminders.ModelReminder{Text: text, LocalDateTime: local, RRule: rr}, fallback, WITA, NOW)
	tu.Must(t, err)
	return r
}

func TestFinishParse(t *testing.T) {
	if r := finish(t, "cek email", "2026-11-10T09:00", nil, ""); r.Echo != "Besok, Rabu 11 Nov, 09.00 WITA" {
		t.Fatal(r.Echo)
	}
	if r := finish(t, "x", "2026-11-09T09:00", nil, ""); r.OK || !strings.Contains(r.Error, "already passed") {
		t.Fatalf("%+v", r)
	}
	if r := finish(t, "les", "2026-11-12T18:00", rr("FREQ=WEEKLY;BYDAY=TU,TH"), ""); *r.FireAt != l2u(t, "2026-11-10T18:00", WITA) || r.Echo != "Tiap Selasa dan Kamis, 18.00 WITA, mulai hari ini" {
		t.Fatalf("%+v", r)
	}
	// "starting next Monday, every weekday 08:00" said on Tuesday 10:00.
	r := finish(t, "standup", "2026-11-16T08:00", rr("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"), "")
	if *r.FireAt != l2u(t, "2026-11-16T08:00", WITA) || r.Echo != "Tiap hari kerja, 08.00 WITA, mulai Senin 16 Nov" {
		t.Fatalf("%+v", r)
	}
	if r := finish(t, "x", "2026-11-16T19:00", rr("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"), ""); *r.FireAt != l2u(t, "2026-11-16T19:00", WITA) {
		t.Fatal("later start moved")
	}
	if r := finish(t, "olahraga", "2026-11-11T07:00", rr("FREQ=DAILY"), ""); r.Echo != "Tiap hari, 07.00 WITA, mulai besok, Rabu 11 Nov" {
		t.Fatal(r.Echo)
	}
	if r := finish(t, "minum", "2026-11-10T13:00", rr("FREQ=HOURLY;INTERVAL=3"), ""); r.Echo != "Tiap 3 jam, mulai hari ini 13.00 WITA" {
		t.Fatal(r.Echo)
	}
	if r := finish(t, "x", "2031-01-01T09:00", nil, ""); r.OK || !strings.Contains(r.Error, "2 years") {
		t.Fatalf("%+v", r)
	}
	if r := finish(t, "x", "2028-11-01T09:00", nil, ""); !r.OK {
		t.Fatalf("%+v", r)
	}
	if r := finish(t, "x", "2026-11-13T18:00", rr("FREQ=WEEKLY"), ""); *r.FireAt != l2u(t, "2026-11-13T18:00", WITA) {
		t.Fatal("anchor moved")
	}
	if r := finish(t, "", "2026-11-11T09:00:00+08:00", rr(""), "the input"); !r.OK || r.Text != "the input" || r.RRule != nil {
		t.Fatalf("%+v", r)
	}
	bad := finish(t, "x", "2026-11-11T09:00", rr("FREQ=SOMETIMES"), "")
	if bad.OK {
		t.Fatal("bad rule ok")
	}
	b, _ := json.Marshal(bad)
	var back reminders.ParsedReminder
	tu.Must(t, json.Unmarshal(b, &back))
	if jsonx.Stringify(back) != string(b) {
		t.Fatal(string(b))
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	p := reminders.BuildSystemPrompt(WITA, NOW)
	for _, s := range []string{"Thursday/Kamis = 2026-11-12", "Next week starts on Monday 2026-11-16", "This month ends on 2026-11-30"} {
		if !strings.Contains(p, s) {
			t.Fatal(s)
		}
	}
}
