package chat

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// --- dashboard.ts ------------------------------------------------------------------------

// DashboardPages is DASHBOARD_PAGES: [path, what the owner does there].
// Keep in sync with apps/app/src/routes/_app (dashboard.test.ts checks it).
var DashboardPages = [][2]string{
	{"/app", "Home: setup checklist and usage"},
	{"/app/connectors/new", "Add a connector: channels (WhatsApp, Telegram), models, memory, tools, MCP servers"},
	{"/app/connectors", "Every connector: turn on or off, see which need fixing"},
	{"/app/connectors/<connectorId>", "One connector: its setup, Test, Connect (QR, sign-in), remove"},
	{"/app/agents", "Agents"},
	{"/app/chats?chat=<Chat ID>", "A chat's transcript (/app/chats for all chats)"},
	{"/app/reminders", "Reminders (?tab=upcoming|recurring|done, ?new=true to add one)"},
	{"/app/jobs", "Scheduled jobs"},
	{"/app/flows", "Flows (/app/flows/<flowId> for one)"},
	{"/app/notes", "Notes and standing instructions"},
	{"/app/attachments", "Files and photos sent in chats"},
	{"/app/activity", "Logs (?tab=events), runs (?tab=runs) and token usage (?tab=usage)"},
	{"/app/settings?tab=general", "Bot language, dashboard language, fallback channel"},
	{"/app/settings?tab=models", "Which model answers, and the fallback chain"},
	{"/app/settings?tab=limits", "Budgets: steps and tools per turn, daily token cap, runs per hour"},
	{"/app/settings?tab=security", "Passkeys and backups"},
	{"/app/settings?tab=hooks", "Inbound webhooks"},
}

var agentSlugLink = regexp.MustCompile(`^[\w-]{1,100}$`)

// DashboardPrompt is dashboardPrompt: where things live in the dashboard.
func DashboardPrompt(appURL, agentSlug string) string {
	base := strings.TrimRight(appURL, "/")
	lines := make([]string, 0, len(DashboardPages)+1)
	for _, p := range DashboardPages {
		lines = append(lines, fmt.Sprintf("- %s%s: %s", base, p[0], p[1]))
	}
	if agentSlug != "" && agentSlugLink.MatchString(agentSlug) {
		own := fmt.Sprintf("- %s/app/agents/%s: Your own settings (?tab=instructions for your persona, ?tab=memory, ?tab=tools, ?tab=chats)", base, agentSlug)
		lines = append(lines[:5], append([]string{own}, lines[5:]...)...)
	}
	head := fmt.Sprintf("Dashboard: the owner's web app at %s/app. When they ask where to set up, add, change or check something, answer with the full link to the right page from this list, not just a page name. Never make up a path that isn't here.", base)
	return strings.Join(append([]string{head}, lines...), "\n")
}

// --- turn-limits.ts ---------------------------------------------------------------------

// WrapUpNote is added after the tool results of the wrap-up step; it never
// reaches the history.
const WrapUpNote = "[System] This turn is almost out of time or steps. Don't call any tool. Reply to the owner now: what you did, what isn't done yet, and what you'd do next. If something is still unfinished, say so plainly and offer to continue."

// StepClock tracks step durations (ms on a monotonic clock).
type StepClock struct {
	StartedAt     float64
	StepStartedAt float64
	LongestStepMs float64
}

// NewStepClock is stepClock.
func NewStepClock(now float64) *StepClock {
	return &StepClock{StartedAt: now, StepStartedAt: now}
}

// WrapUpInput is shouldWrapUp's options.
type WrapUpInput struct {
	StepNumber int
	MaxSteps   int
	TimeoutMs  int64
	Now        float64
}

// ShouldWrapUp is shouldWrapUp, called at the start of each step: "steps" or
// "time" when this step should answer without tools, "" otherwise.
func ShouldWrapUp(c *StepClock, o WrapUpInput) string {
	if o.StepNumber > 0 {
		c.LongestStepMs = math.Max(c.LongestStepMs, o.Now-c.StepStartedAt)
	}
	c.StepStartedAt = o.Now
	if o.StepNumber == 0 {
		return ""
	}
	if o.StepNumber >= o.MaxSteps-1 {
		return "steps"
	}
	timeout := float64(o.TimeoutMs)
	reserve := math.Max(c.LongestStepMs, timeout*0.1)
	if o.Now-c.StartedAt+reserve > timeout*0.8 {
		return "time"
	}
	return ""
}

// RanTool is a tool that finished this turn.
type RanTool struct {
	Name  string
	Write bool
	OK    bool
}

// FormatLimit is formatLimit: "10 menit" or "45 detik".
func FormatLimit(ms int64) string {
	if ms >= 60_000 && ms%60_000 == 0 {
		return fmt.Sprintf("%d menit", ms/60_000)
	}
	return fmt.Sprintf("%d detik", int64(math.Floor(float64(ms)/1000+0.5)))
}

func counted(names []string) string {
	n := map[string]int{}
	var order []string
	for _, x := range names {
		if _, ok := n[x]; !ok {
			order = append(order, x)
		}
		n[x]++
	}
	parts := make([]string, len(order))
	for i, k := range order {
		if n[k] > 1 {
			parts[i] = fmt.Sprintf("%s (%dx)", k, n[k])
		} else {
			parts[i] = k
		}
	}
	if len(parts) > 6 {
		return fmt.Sprintf("%s, +%d lagi", strings.Join(parts[:6], ", "), len(parts)-6)
	}
	return strings.Join(parts, ", ")
}

// Limit is limitNotice's first argument: TimeoutMs when the turn timed out,
// otherwise MaxSteps.
type Limit struct {
	TimeoutMs int64
	MaxSteps  int64
	Steps     bool
}

// LimitNotice is limitNotice: what the owner sees when a turn hits a limit with no reply.
func LimitNotice(limit Limit, ran []RanTool) string {
	what := fmt.Sprintf("udah lewat batas %s", FormatLimit(limit.TimeoutMs))
	if limit.Steps {
		what = fmt.Sprintf("udah kena batas %d langkah", limit.MaxSteps)
	}
	lines := []string{fmt.Sprintf("⏱️ Giliranku kepotong: %s sebelum aku sempat balas.", what)}
	if len(ran) > 0 {
		names := make([]string, len(ran))
		for i, r := range ran {
			names[i] = r.Name
		}
		lines = append(lines, fmt.Sprintf("Yang sempat jalan: %s.", counted(names)))
	}
	var changed []string
	for _, r := range ran {
		if r.Write && r.OK {
			changed = append(changed, r.Name)
		}
	}
	if len(changed) > 0 {
		lines = append(lines, fmt.Sprintf("Yang udah ngubah sesuatu: %s.", counted(changed)))
	} else {
		lines = append(lines, "Belum ada yang diubah.")
	}
	lines = append(lines, `Bilang "lanjut" kalau mau aku terusin.`)
	return strings.Join(lines, "\n")
}
