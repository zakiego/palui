package proactive

import (
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// MaxMessageChars caps a proactive message (RFC 2).
const MaxMessageChars = 700

// MaxFieldChars caps one value shown in a line.
const MaxFieldChars = 200

var (
	ctlAll   = regexp.MustCompile(`[\x00-\x1f\x7f\x{2028}\x{2029}]`)
	ctlKeep  = regexp.MustCompile(`[\x00-\x09\x0b-\x1f\x7f\x{2028}\x{2029}]`)
	manyNL   = regexp.MustCompile(`\n{3,}`)
	manySp   = regexp.MustCompile(`[ \t]{2,}`)
	markdown = regexp.MustCompile("[*_`~]")
)

// Snapshot is what a nudge says, kept on its row. Name and Title are what
// the fixed sources of schema 1 kept, read so old rows still have a label.
type Snapshot struct {
	Text   string `json:"text,omitempty"`
	Urgent bool   `json:"urgent,omitempty"`
	// Summaries: the date they cover ("2026-11-24").
	Date  string `json:"date,omitempty"`
	Name  string `json:"name,omitempty"`
	Title string `json:"title,omitempty"`
}

// Field cleans one value for a line: one line, plain, capped.
func Field(s string) string {
	s = ctlAll.ReplaceAllString(s, " ")
	s = markdown.ReplaceAllString(s, "")
	s = strings.TrimSpace(manySp.ReplaceAllString(s, " "))
	if strx.Len16(s) > MaxFieldChars {
		return strx.Slice16(s, MaxFieldChars-1) + "…"
	}
	return s
}

// MessageText cleans the model's wording: plain text, line breaks kept,
// capped at MaxMessageChars (it was written after reading untrusted data).
func MessageText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = ctlKeep.ReplaceAllString(s, " ")
	s = manyNL.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if strx.Len16(s) > MaxMessageChars {
		return strx.Slice16(s, MaxMessageChars-1) + "…"
	}
	return s
}

func loadTZ(tz string) *time.Location {
	if l, err := time.LoadLocation(tz); err == nil && tz != "" {
		return l
	}
	return time.UTC
}

func clock(ms int64, tz string) string { return reminders.FormatClock(ms, tz) }

// when is "jam 14.30", "besok jam 09.00" or "Senin 30 Nov jam 09.00".
func when(ms int64, tz string, now int64) string {
	c := "jam " + clock(ms, tz)
	switch reminders.FormatDate(ms, tz, now) {
	case reminders.FormatDate(now, tz, now):
		return c
	case reminders.FormatDate(now+24*3600_000, tz, now):
		return "besok " + c
	}
	return reminders.FormatDate(ms, tz, now) + " " + c
}

func snapshotOf(n db.Nudge) Snapshot {
	var s Snapshot
	_ = n.Snapshot.Decode(&s)
	return s
}

// Label is a short name for a nudge in replies, the activity list and the
// mute list.
func Label(n db.Nudge) string {
	s := snapshotOf(n)
	switch {
	case s.Text != "":
		return clip(Field(s.Text), 120)
	case s.Name != "":
		return Field(s.Name)
	case s.Title != "":
		return Field(s.Title)
	}
	return n.Subject
}

// Buttons is the owner's actions on a proactive message (RFC 2): they apply
// to every item the message carries.
func Buttons(first db.Nudge, lang string) []db.OutboxAction {
	id := first.ID
	labels := [5]string{"Selesai", "1j", "3j", "Besok", "Jangan ingetin"}
	if lang == "en" {
		labels = [5]string{"Done", "1h", "3h", "Tomorrow", "Stop these"}
	}
	return []db.OutboxAction{
		{ID: "ndg:done:" + id, Label: labels[0]},
		{ID: "ndg:snooze:" + id + ":1h", Label: labels[1]},
		{ID: "ndg:snooze:" + id + ":3h", Label: labels[2]},
		{ID: "ndg:snooze:" + id + ":tomorrow", Label: labels[3]},
		{ID: "ndg:mute:" + id, Label: labels[4]},
	}
}

// agentModel resolves the agent's chain for a role, metered to the global and
// the agent's daily token counts. ok false: no model is running, or a budget
// says stop.
func agentModel(q db.Q, host models.ModelHost, a db.Agent, role string, now int64, clock func() int64, tokens *int64) (models.LanguageModel, bool, error) {
	if host == nil {
		return nil, false, nil
	}
	if c, err := budget.CheckGlobal(q, now); err != nil {
		return nil, false, err
	} else if !c.OK {
		return nil, false, nil
	}
	if a.DailyTokenCap > 0 {
		over, err := budget.AgentOverCap(q, a.ID, a.DailyTokenCap, now)
		if err != nil {
			return nil, false, err
		}
		if over {
			return nil, false, nil
		}
	}
	chain, err := models.ResolveAgentChain(q, host, a.ModelRoles.V, role)
	if err != nil {
		return nil, false, err
	}
	if len(chain) == 0 {
		return nil, false, nil
	}
	fm, err := models.NewFailoverModel(chain, nil)
	if err != nil {
		return nil, false, err
	}
	return &models.MeteredModel{Model: fm, OnTokens: func(n int64) {
		*tokens += n
		at := clock()
		_ = budget.RecordRunTokens(q, n, at)
		_ = budget.RecordAgentTokens(q, a.ID, n, at)
	}}, true, nil
}
