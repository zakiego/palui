// Package budget ports packages/core/src/budget: per turn steps, wall clock and
// tool count (enforced by the turn); global daily tokens and runs per hour; per
// job daily tokens. Counters live in the `usage` table so they survive restarts.
package budget

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

func zoned(now int64, tz string) time.Time {
	return time.UnixMilli(now).In(zone.MustLoad(tz))
}

// DayKey is the local date "yyyy-LL-dd" in tz.
func DayKey(now int64, tz string) string { return zoned(now, tz).Format("2006-01-02") }

// HourKey is "yyyy-LL-dd'T'HH" in tz.
func HourKey(now int64, tz string) string { return zoned(now, tz).Format("2006-01-02T15") }

func TokensKey(now int64, tz string) string { return "tokens:" + DayKey(now, tz) }
func RunsKey(now int64, tz string) string   { return "runs:" + HourKey(now, tz) }
func JobKey(jobID string, now int64, tz string) string {
	return "job:" + jobID + ":" + DayKey(now, tz)
}
func AgentKey(agentID string, now int64, tz string) string {
	return "agent:" + agentID + ":" + DayKey(now, tz)
}
func AgentNoticeKey(agentID string, now int64, tz string) string {
	return "agent-notice:" + agentID + ":" + DayKey(now, tz)
}

func GetUsage(q db.Q, key string) (int64, error) {
	var v int64
	err := q.QueryRow("SELECT value FROM usage WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// AddUsage adds n to a counter and returns the new value.
func AddUsage(q db.Q, key string, n int64, now int64) (int64, error) {
	if n == 0 {
		return GetUsage(q, key)
	}
	var v int64
	err := q.WQueryRow("INSERT INTO usage (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO UPDATE SET value = usage.value + ?, updated_at = ? RETURNING value", key, n, now, n, now).Scan(&v)
	return v, err
}

// Check is a BudgetCheck: OK, or paused until Until with Reason. Fresh is true
// only the first time a pause is seen, so the caller tells the owner once.
type Check struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	Until  int64  `json:"until,omitempty"`
	Fresh  bool   `json:"fresh,omitempty"`
}

// nextHour and nextDay follow luxon's startOf/plus, DST gaps included.
func nextHour(now int64, tz string) int64 { return zone.NextHour(now, zone.MustLoad(tz)) }

func nextDay(now int64, tz string) int64 { return zone.NextDay(now, zone.MustLoad(tz)) }

// FormatClock is "HH.mm" in tz.
func FormatClock(ms int64, tz string) string { return zoned(ms, tz).Format("15.04") }

// formatID is toLocaleString("id-ID") for an integer: dots between thousands.
func formatID(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := false
	if n < 0 {
		neg = true
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// Pause is the cap that blocks turns right now.
type Pause struct {
	Reason string
	Until  int64
}

// ActivePause reads the live counters and current settings. Pure: a raised cap
// lifts a pause on the next check, a lowered one starts it. A daily token cap
// of 0 (or less) means no cap.
func ActivePause(q db.Q, now int64) (*Pause, error) {
	tz, err := settings.Timezone(q)
	if err != nil {
		return nil, err
	}
	b, err := settings.GetBudgets(q)
	if err != nil {
		return nil, err
	}
	runs, err := GetUsage(q, RunsKey(now, tz))
	if err != nil {
		return nil, err
	}
	if runs >= b.MaxRunsPerHour {
		return &Pause{Until: nextHour(now, tz), Reason: fmt.Sprintf("batas %d run per jam kena", b.MaxRunsPerHour)}, nil
	}
	if b.DailyTokenCap > 0 {
		tokens, err := GetUsage(q, TokensKey(now, tz))
		if err != nil {
			return nil, err
		}
		if tokens >= b.DailyTokenCap {
			return &Pause{Until: nextDay(now, tz), Reason: fmt.Sprintf("batas %s token per hari kena", formatID(b.DailyTokenCap))}, nil
		}
	}
	return nil, nil
}

// CheckGlobal says whether a new turn may start. A hit cap pauses turns until
// the window rolls over; the pause is mirrored in settings so Fresh is true
// only the first time.
func CheckGlobal(q db.Q, now int64) (Check, error) {
	hit, err := ActivePause(q, now)
	if err != nil {
		return Check{}, err
	}
	until, err := settings.Get[*int64](q, "turnsPausedUntil")
	if err != nil {
		return Check{}, err
	}
	if hit == nil {
		if until != nil {
			if err := settings.Set(q, "turnsPausedUntil", nil, now); err != nil {
				return Check{}, err
			}
			if err := settings.Set(q, "turnsPausedReason", nil, now); err != nil {
				return Check{}, err
			}
		}
		return Check{OK: true}, nil
	}
	reason, err := settings.Get[*string](q, "turnsPausedReason")
	if err != nil {
		return Check{}, err
	}
	if until != nil && *until == hit.Until && reason != nil && *reason == hit.Reason {
		return Check{Reason: hit.Reason, Until: hit.Until}, nil
	}
	return PauseTurns(q, hit.Until, hit.Reason, now)
}

func PauseTurns(q db.Q, until int64, reason string, now int64) (Check, error) {
	if err := settings.Set(q, "turnsPausedUntil", until, now); err != nil {
		return Check{}, err
	}
	if err := settings.Set(q, "turnsPausedReason", reason, now); err != nil {
		return Check{}, err
	}
	return Check{Reason: reason, Until: until, Fresh: true}, nil
}

func PausedNotice(q db.Q, c Check) (string, error) {
	tz, err := settings.Timezone(q)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Turn lagi dijeda sampai %s: %s. Command kayak /status tetap jalan.", FormatClock(c.Until, tz), c.Reason), nil
}

// JobOverCap: per-job usage is keyed by the day in the job's own zone (tz, ""
// for the settings zone). A cap of 0 (or less) means no cap.
func JobOverCap(q db.Q, jobID string, cap int64, now int64, tz string) (bool, error) {
	if cap <= 0 {
		return false, nil
	}
	if tz == "" {
		var err error
		if tz, err = settings.Timezone(q); err != nil {
			return false, err
		}
	}
	used, err := GetUsage(q, JobKey(jobID, now, tz))
	return used >= cap, err
}

// CacheReadWeight: tokens read from the prompt cache count for a tenth.
const CacheReadWeight = 0.1

// Usage is one model call's token counts. TokensIn is all input, cached or not.
type Usage struct {
	TokensIn     int64
	TokensOut    int64
	TokensCached *int64
}

// jsRound is Math.round: halves round up.
func jsRound(f float64) int64 { return int64(math.Floor(f + 0.5)) }

// Tokens is what a model call adds to the daily counters: fresh input, cache
// writes and output in full, cache reads at CacheReadWeight.
func Tokens(u Usage) int64 {
	var c int64
	if u.TokensCached != nil {
		c = *u.TokensCached
	}
	cached := min(max(c, 0), max(u.TokensIn, 0))
	return u.TokensIn - cached + jsRound(float64(cached)*CacheReadWeight) + u.TokensOut
}

// RecordRunTokens adds tokens to the global daily counter. The job runner adds
// the same tokens to the per-job counter, so this never writes it.
func RecordRunTokens(q db.Q, tokens int64, now int64) error {
	tz, err := settings.Timezone(q)
	if err != nil {
		return err
	}
	_, err = AddUsage(q, TokensKey(now, tz), tokens, now)
	return err
}

func RecordRunStart(q db.Q, now int64) error {
	tz, err := settings.Timezone(q)
	if err != nil {
		return err
	}
	_, err = AddUsage(q, RunsKey(now, tz), 1, now)
	return err
}

func TokensToday(q db.Q, now int64) (int64, error) {
	tz, err := settings.Timezone(q)
	if err != nil {
		return 0, err
	}
	return GetUsage(q, TokensKey(now, tz))
}

// AgentOverCap is the per-agent daily cap, in the settings zone. 0 means none.
func AgentOverCap(q db.Q, agentID string, dailyTokenCap int64, now int64) (bool, error) {
	if dailyTokenCap <= 0 {
		return false, nil
	}
	used, err := AgentTokensToday(q, agentID, now)
	return used >= dailyTokenCap, err
}

func RecordAgentTokens(q db.Q, agentID string, tokens int64, now int64) error {
	tz, err := settings.Timezone(q)
	if err != nil {
		return err
	}
	_, err = AddUsage(q, AgentKey(agentID, now, tz), tokens, now)
	return err
}

func AgentTokensToday(q db.Q, agentID string, now int64) (int64, error) {
	tz, err := settings.Timezone(q)
	if err != nil {
		return 0, err
	}
	return GetUsage(q, AgentKey(agentID, now, tz))
}

// FirstAgentNoticeToday is true the first time it's called for this agent today.
func FirstAgentNoticeToday(q db.Q, agentID string, now int64) (bool, error) {
	tz, err := settings.Timezone(q)
	if err != nil {
		return false, err
	}
	n, err := AddUsage(q, AgentNoticeKey(agentID, now, tz), 1, now)
	return n == 1, err
}
