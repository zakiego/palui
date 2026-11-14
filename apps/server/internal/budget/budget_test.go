package budget_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ports packages/core/src/budget/budget.test.ts.

var NOW = testutil.NOW

const TZ = "Asia/Makassar"

func i64(v int64) *int64 { return &v }

func setCap(t *testing.T, d db.Q, cap int64) {
	t.Helper()
	b, _ := settings.GetBudgets(d)
	b.DailyTokenCap = cap
	testutil.Must(t, settings.Set(d, "budgets", b, NOW))
}

func add(t *testing.T, d db.Q, key string, n int64) {
	t.Helper()
	_, err := budget.AddUsage(d, key, n, NOW)
	testutil.Must(t, err)
}

func check(t *testing.T, d db.Q, now int64) budget.Check {
	t.Helper()
	c, err := budget.CheckGlobal(d, now)
	testutil.Must(t, err)
	return c
}

func TestJobOverCapTreatsZeroAsUnlimited(t *testing.T) {
	d := testutil.DB(t)
	if over, _ := budget.JobOverCap(d, "job_1", 0, NOW, TZ); over {
		t.Fatal("over")
	}
	add(t, d, budget.JobKey("job_1", NOW, TZ), 50_000)
	if over, _ := budget.JobOverCap(d, "job_1", 0, NOW, TZ); over {
		t.Fatal("over")
	}
}

func TestJobOverCapOnceUsageReachesAPositiveCap(t *testing.T) {
	d := testutil.DB(t)
	if over, _ := budget.JobOverCap(d, "job_1", 1000, NOW, TZ); over {
		t.Fatal("over")
	}
	add(t, d, budget.JobKey("job_1", NOW, TZ), 1000)
	if over, _ := budget.JobOverCap(d, "job_1", 1000, NOW, TZ); !over {
		t.Fatal("not over")
	}
}

func TestPausesOnceWhenTheDailyCapIsHitThenStaysQuiet(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", TZ, NOW))
	setCap(t, d, 2_000_000)
	add(t, d, budget.TokensKey(NOW, TZ), 2_029_956)
	if c := check(t, d, NOW); c.OK || !c.Fresh {
		t.Fatalf("%+v", c)
	}
	if c := check(t, d, NOW+1000); c.OK || c.Fresh {
		t.Fatalf("%+v", c)
	}
}

func TestLiftsThePauseWhenTheCapIsRaised(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", TZ, NOW))
	setCap(t, d, 2_000_000)
	add(t, d, budget.TokensKey(NOW, TZ), 2_029_956)
	if check(t, d, NOW).OK {
		t.Fatal("ok")
	}
	setCap(t, d, 10_000_000)
	if p, _ := budget.ActivePause(d, NOW); p != nil {
		t.Fatal(p)
	}
	if !check(t, d, NOW).OK {
		t.Fatal("still paused")
	}
	if v, _ := settings.Get[*int64](d, "turnsPausedUntil"); v != nil {
		t.Fatal("until kept")
	}
	if v, _ := settings.Get[*string](d, "turnsPausedReason"); v != nil {
		t.Fatal("reason kept")
	}
}

func TestNamesTheNewCapWhenLoweredUnderAPause(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", TZ, NOW))
	setCap(t, d, 2_000_000)
	add(t, d, budget.TokensKey(NOW, TZ), 2_029_956)
	check(t, d, NOW)
	setCap(t, d, 1_000_000)
	if c := check(t, d, NOW); c.OK || !c.Fresh {
		t.Fatalf("%+v", c)
	}
	if r, _ := settings.Get[*string](d, "turnsPausedReason"); r == nil || !strings.Contains(*r, "1.000.000") {
		t.Fatal(r)
	}
}

func TestClearsTheTokenPauseAtTheNextDay(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", TZ, NOW))
	b, _ := settings.GetBudgets(d)
	add(t, d, budget.TokensKey(NOW, TZ), b.DailyTokenCap)
	paused := check(t, d, NOW)
	if paused.OK {
		t.Fatal("expected a pause")
	}
	if !check(t, d, paused.Until).OK {
		t.Fatal("still paused tomorrow")
	}
	if v, _ := settings.Get[*int64](d, "turnsPausedUntil"); v != nil {
		t.Fatal("until kept")
	}
}

func TestNeverPausesWithCapZero(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", TZ, NOW))
	setCap(t, d, 0)
	add(t, d, budget.TokensKey(NOW, TZ), 500_000_000)
	if c := check(t, d, NOW); c != (budget.Check{OK: true}) {
		t.Fatalf("%+v", c)
	}
	if v, _ := settings.Get[*int64](d, "turnsPausedUntil"); v != nil {
		t.Fatal("until set")
	}
}

func TestPausesUntilTomorrowOnceAPositiveCapIsReached(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", TZ, NOW))
	setCap(t, d, 10_000)
	add(t, d, budget.TokensKey(NOW, TZ), 9_999)
	if c := check(t, d, NOW); !c.OK {
		t.Fatalf("%+v", c)
	}
	add(t, d, budget.TokensKey(NOW, TZ), 1)
	c := check(t, d, NOW)
	if c.OK || !c.Fresh || c.Until != time.Date(2026, 11, 10, 16, 0, 0, 0, time.UTC).UnixMilli() || !strings.Contains(c.Reason, "token per hari") {
		t.Fatalf("%+v", c)
	}
	notice, _ := budget.PausedNotice(d, c)
	if notice != "Turn lagi dijeda sampai 00.00: batas 10.000 token per hari kena. Command kayak /status tetap jalan." {
		t.Fatal(notice)
	}
}

func TestRunsPerHourCap(t *testing.T) {
	d := testutil.DB(t)
	b, _ := settings.GetBudgets(d)
	b.MaxRunsPerHour = 2
	testutil.Must(t, settings.Set(d, "budgets", b, NOW))
	testutil.Must(t, budget.RecordRunStart(d, NOW))
	testutil.Must(t, budget.RecordRunStart(d, NOW))
	c := check(t, d, NOW)
	if c.OK || c.Reason != "batas 2 run per jam kena" || c.Until != NOW+time.Hour.Milliseconds() {
		t.Fatalf("%+v", c)
	}
}

func TestBudgetTokensCountsCacheReadsAtATenth(t *testing.T) {
	cases := []struct {
		u    budget.Usage
		want int64
	}{
		{budget.Usage{TokensIn: 52_000, TokensOut: 800, TokensCached: i64(40_000)}, 12_000 + 4_000 + 800},
		{budget.Usage{TokensIn: 52_000, TokensOut: 800, TokensCached: i64(0)}, 52_800},
		{budget.Usage{TokensIn: 52_000, TokensOut: 800}, 52_800},
		{budget.Usage{TokensIn: 50_000, TokensCached: i64(50_000)}, 5_000},
		{budget.Usage{TokensIn: 15, TokensCached: i64(15)}, 2},
		{budget.Usage{TokensIn: 100, TokensOut: 5, TokensCached: i64(900)}, 15},
		{budget.Usage{TokensIn: 100, TokensOut: 5, TokensCached: i64(-3)}, 105},
	}
	for _, c := range cases {
		if got := budget.Tokens(c.u); got != c.want {
			t.Errorf("%+v: %d, want %d", c.u, got, c.want)
		}
	}
}

func TestThe20261111Measurement(t *testing.T) {
	counted := budget.Tokens(budget.Usage{TokensIn: 1_958_748, TokensCached: i64(1_275_047)})
	if counted != 683_701+127_505 {
		t.Fatal(counted)
	}
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "timezone", TZ, NOW))
	testutil.Must(t, budget.RecordRunTokens(d, counted, NOW))
	testutil.Must(t, budget.RecordAgentTokens(d, "agent_1", counted, NOW))
	if n, _ := budget.TokensToday(d, NOW); n != 811_206 {
		t.Fatal(n)
	}
	if n, _ := budget.AgentTokensToday(d, "agent_1", NOW); n != 811_206 {
		t.Fatal(n)
	}
	if c := check(t, d, NOW); !c.OK {
		t.Fatalf("%+v", c)
	}
	if over, _ := budget.AgentOverCap(d, "agent_1", 800_000, NOW); !over {
		t.Fatal("agent cap")
	}
	first, _ := budget.FirstAgentNoticeToday(d, "agent_1", NOW)
	second, _ := budget.FirstAgentNoticeToday(d, "agent_1", NOW)
	if !first || second {
		t.Fatal("notice")
	}
}

func TestKeys(t *testing.T) {
	if budget.DayKey(NOW, TZ) != "2026-11-10" || budget.HourKey(NOW, TZ) != "2026-11-10T10" || budget.FormatClock(NOW, TZ) != "10.00" {
		t.Fatal(budget.HourKey(NOW, TZ))
	}
}
