package reminders_test

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

// Goldens from testdata/gen/time.ts (the real TS functions).

type result[T any] struct {
	OK        *T      `json:"ok"`
	Error     *string `json:"error"`
	TimeError bool    `json:"timeError"`
}

type sched struct {
	RRule      *string `json:"rrule"`
	TZ         string  `json:"tz"`
	LocalStart string  `json:"localStart"`
}

func (s sched) s() reminders.Schedule {
	return reminders.Schedule{RRule: s.RRule, TZ: s.TZ, LocalStart: s.LocalStart}
}

type timeGolden struct {
	Now        int64 `json:"now"`
	ZoneChecks []struct {
		TZ    string `json:"tz"`
		Valid bool   `json:"valid"`
	} `json:"zoneChecks"`
	Zones []struct {
		TZ    string `json:"tz"`
		ToUTC []struct {
			Local string `json:"local"`
			result[int64]
		} `json:"toUtc"`
		FromUTC []struct {
			Ms           int64  `json:"ms"`
			Local        string `json:"local"`
			ISO          string `json:"iso"`
			Label        string `json:"label"`
			Clock        string `json:"clock"`
			Date         string `json:"date"`
			When         string `json:"when"`
			Tomorrow     int64  `json:"tomorrow"`
			Tomorrow1830 int64  `json:"tomorrow1830"`
			StartOfDay   int64  `json:"startOfDay"`
			NextDay      int64  `json:"nextDay"`
			NextHour     int64  `json:"nextHour"`
		} `json:"fromUtc"`
	} `json:"zones"`
	Parse []struct {
		Input      string                       `json:"input"`
		Parsed     result[reminders.LocalParts] `json:"parsed"`
		Normalized result[string]               `json:"normalized"`
	} `json:"parse"`
	RRules []struct {
		Input string `json:"input"`
		result[string]
	} `json:"rrules"`
	Occurrences []struct {
		Schedule sched `json:"schedule"`
		After    []struct {
			After int64 `json:"after"`
			result[[]int64]
		} `json:"after"`
		NextAt []struct {
			After int64 `json:"after"`
			result[*int64]
		} `json:"nextAt"`
		ZeroN result[[]int64] `json:"zeroN"`
	} `json:"occurrences"`
	Latest []struct {
		Schedule sched `json:"schedule"`
		From     int64 `json:"from"`
		At       int64 `json:"at"`
		result[int64]
	} `json:"latest"`
	Echoes []struct {
		Schedule    sched          `json:"schedule"`
		Now         int64          `json:"now"`
		Describe    result[string] `json:"describe"`
		Echo        result[string] `json:"echo"`
		SeriesStart result[string] `json:"seriesStart"`
	} `json:"echoes"`
	FirstFires []struct {
		Schedule sched `json:"schedule"`
		Now      int64 `json:"now"`
		result[struct {
			Schedule sched `json:"schedule"`
			FireAt   int64 `json:"fireAt"`
			Rolled   bool  `json:"rolled"`
		}]
	} `json:"firstFires"`
	Parses []struct {
		Model struct {
			LocalDateTime string  `json:"localDateTime"`
			RRule         *string `json:"rrule"`
		} `json:"model"`
		TZ     string `json:"tz"`
		Now    int64  `json:"now"`
		Result struct {
			OK        bool    `json:"ok"`
			FireAt    int64   `json:"fireAt"`
			FireAtISO string  `json:"fireAtIso"`
			RRule     *string `json:"rrule"`
			Echo      string  `json:"echo"`
			Error     string  `json:"error"`
		} `json:"result"`
	} `json:"parses"`
}

func loadTimeGolden(t *testing.T) timeGolden {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/time.json")
	if err != nil {
		t.Fatal(err)
	}
	var g timeGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

// checkErr compares an error with the golden's: same message, same kind.
func checkErr(t *testing.T, what string, err error, want *string, timeErr bool) bool {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Errorf("%s: unexpected error %v", what, err)
			return false
		}
		return true
	}
	if err == nil {
		t.Errorf("%s: want error %q", what, *want)
		return false
	}
	var te *reminders.TimeError
	if err.Error() != *want || errors.As(err, &te) != timeErr {
		t.Errorf("%s: error %q (time error %v), want %q (%v)", what, err.Error(), errors.As(err, &te), *want, timeErr)
	}
	return false
}

func TestGoldenZones(t *testing.T) {
	g := loadTimeGolden(t)
	for _, c := range g.ZoneChecks {
		if reminders.IsValidZone(c.TZ) != c.Valid {
			t.Errorf("IsValidZone(%q) = %v, want %v", c.TZ, !c.Valid, c.Valid)
		}
	}
	n := 0
	for _, z := range g.Zones {
		loc := zone.MustLoad(z.TZ)
		for _, c := range z.ToUTC {
			got, err := reminders.LocalToUTC(c.Local, z.TZ)
			if checkErr(t, z.TZ+" "+c.Local, err, c.Error, c.TimeError) && got != *c.OK {
				t.Errorf("LocalToUTC(%s, %s) = %s, want %s", c.Local, z.TZ, reminders.UTCToISO(got, z.TZ), reminders.UTCToISO(*c.OK, z.TZ))
			}
			n++
		}
		for _, c := range z.FromUTC {
			check := func(name, got, want string) {
				if got != want {
					t.Errorf("%s %s at %d: %q, want %q", z.TZ, name, c.Ms, got, want)
				}
			}
			checkN := func(name string, got, want int64) {
				if got != want {
					t.Errorf("%s %s at %d: %d, want %d", z.TZ, name, c.Ms, got, want)
				}
			}
			check("local", reminders.UTCToLocal(c.Ms, z.TZ), c.Local)
			check("iso", reminders.UTCToISO(c.Ms, z.TZ), c.ISO)
			check("label", reminders.ZoneLabel(z.TZ, c.Ms), c.Label)
			check("clock", reminders.FormatClock(c.Ms, z.TZ), c.Clock)
			check("date", reminders.FormatDate(c.Ms, z.TZ, g.Now), c.Date)
			check("when", reminders.FormatWhen(c.Ms, z.TZ, g.Now), c.When)
			checkN("tomorrowAt", reminders.TomorrowAt(c.Ms, z.TZ, 9, 0), c.Tomorrow)
			checkN("tomorrowAt 18:30", reminders.TomorrowAt(c.Ms, z.TZ, 18, 30), c.Tomorrow1830)
			checkN("startOfDay", zone.StartOfDay(c.Ms, loc), c.StartOfDay)
			checkN("nextDay", zone.NextDay(c.Ms, loc), c.NextDay)
			checkN("nextHour", zone.NextHour(c.Ms, loc), c.NextHour)
			n++
		}
	}
	if n < 500 {
		t.Fatalf("only %d zone cases", n)
	}
}

func TestGoldenParseLocal(t *testing.T) {
	g := loadTimeGolden(t)
	for _, c := range g.Parse {
		p, err := reminders.ParseLocal(c.Input)
		if checkErr(t, "parse "+c.Input, err, c.Parsed.Error, c.Parsed.TimeError) && p != *c.Parsed.OK {
			t.Errorf("ParseLocal(%q) = %+v, want %+v", c.Input, p, *c.Parsed.OK)
		}
		s, err := reminders.NormalizeLocal(c.Input)
		if checkErr(t, "normalize "+c.Input, err, c.Normalized.Error, c.Normalized.TimeError) && s != *c.Normalized.OK {
			t.Errorf("NormalizeLocal(%q) = %q, want %q", c.Input, s, *c.Normalized.OK)
		}
	}
}

func TestGoldenNormalizeRrule(t *testing.T) {
	g := loadTimeGolden(t)
	for _, c := range g.RRules {
		got, err := reminders.NormalizeRrule(c.Input)
		if checkErr(t, "rrule "+c.Input, err, c.Error, c.TimeError) && got != *c.OK {
			t.Errorf("NormalizeRrule(%q) = %q, want %q", c.Input, got, *c.OK)
		}
	}
}

func TestGoldenOccurrences(t *testing.T) {
	g := loadTimeGolden(t)
	for _, c := range g.Occurrences {
		s := c.Schedule.s()
		for _, a := range c.After {
			got, err := reminders.OccurrencesAfter(s, a.After, 6)
			if checkErr(t, "occurrences", err, a.Error, a.TimeError) && !equalInts(got, *a.OK) {
				t.Errorf("OccurrencesAfter(%+v, %d) =\n %s\nwant\n %s", c.Schedule, a.After, isos(got, s.TZ), isos(*a.OK, s.TZ))
			}
		}
		for _, a := range c.NextAt {
			got, ok, err := reminders.NextOccurrence(s, a.After)
			if !checkErr(t, "next", err, a.Error, a.TimeError) {
				continue
			}
			var want *int64
			if a.OK != nil {
				want = *a.OK
			}
			if ok != (want != nil) || (ok && got != *want) {
				t.Errorf("NextOccurrence(%+v, %d) = %d %v, want %v", c.Schedule, a.After, got, ok, want)
			}
		}
		got, err := reminders.OccurrencesAfter(s, g.Now, 0)
		if checkErr(t, "zero", err, c.ZeroN.Error, c.ZeroN.TimeError) && len(got) != 0 {
			t.Errorf("n=0 gave %v", got)
		}
	}
	for _, c := range g.Latest {
		got, err := reminders.LatestAtOrBefore(c.Schedule.s(), c.From, c.At)
		if checkErr(t, "latest", err, c.Error, c.TimeError) && got != *c.OK {
			t.Errorf("LatestAtOrBefore(%+v) = %d, want %d", c.Schedule, got, *c.OK)
		}
	}
}

func TestGoldenEchoes(t *testing.T) {
	g := loadTimeGolden(t)
	for _, c := range g.Echoes {
		s := c.Schedule.s()
		d, err := reminders.DescribeRecurrence(s, c.Now)
		if checkErr(t, "describe", err, c.Describe.Error, c.Describe.TimeError) && d != *c.Describe.OK {
			t.Errorf("DescribeRecurrence(%+v, %d) = %q, want %q", c.Schedule, c.Now, d, *c.Describe.OK)
		}
		next, ok, _ := reminders.NextOccurrence(s, c.Now)
		if !ok {
			next = 0
		}
		e, err := reminders.FormatEcho(s, next, c.Now)
		if checkErr(t, "echo", err, c.Echo.Error, c.Echo.TimeError) && e != *c.Echo.OK {
			t.Errorf("FormatEcho = %q, want %q", e, *c.Echo.OK)
		}
		first, ok, _ := reminders.NextOccurrence(s, c.Now-1)
		if !ok {
			first = c.Now
		}
		ss, err := reminders.FormatSeriesStart(s, first, c.Now)
		if checkErr(t, "series start", err, c.SeriesStart.Error, c.SeriesStart.TimeError) && ss != *c.SeriesStart.OK {
			t.Errorf("FormatSeriesStart(%+v) = %q, want %q", c.Schedule, ss, *c.SeriesStart.OK)
		}
	}
}

func TestGoldenFirstFire(t *testing.T) {
	g := loadTimeGolden(t)
	for _, c := range g.FirstFires {
		s := c.Schedule.s()
		var err error
		s.LocalStart, err = reminders.NormalizeLocal(s.LocalStart)
		if err != nil {
			t.Fatal(err)
		}
		got, err := reminders.ResolveFirstFire(s, c.Now)
		if !checkErr(t, "first fire", err, c.Error, c.TimeError) {
			continue
		}
		w := c.OK
		if got.FireAt != w.FireAt || got.Rolled != w.Rolled || got.Schedule.LocalStart != w.Schedule.LocalStart {
			t.Errorf("ResolveFirstFire(%+v, %d) = %+v, want %+v", c.Schedule, c.Now, got, *w)
		}
	}
}

var offsetSuffix = regexp.MustCompile(`(?i)(?:Z|[+-]\d{2}:?\d{2})$`)

// finishParse from reminders/parse.ts without the model: the composition M4
// ports, here to check TodayInstead against the TS.
func finishParse(localDateTime string, rr *string, tz string, now int64) (fireAt int64, iso string, echo string, err error) {
	if err := reminders.AssertZone(tz); err != nil {
		return 0, "", "", err
	}
	localStart, err := reminders.NormalizeLocal(offsetSuffix.ReplaceAllString(strx.Trim(localDateTime), ""))
	if err != nil {
		return 0, "", "", err
	}
	var rule *string
	if rr != nil && strings.TrimSpace(*rr) != "" {
		n, err := reminders.NormalizeRrule(*rr)
		if err != nil {
			return 0, "", "", err
		}
		rule = &n
	}
	ff, err := reminders.ResolveFirstFire(reminders.Schedule{RRule: rule, TZ: tz, LocalStart: localStart}, now)
	if err != nil {
		return 0, "", "", err
	}
	s, fireAt := ff.Schedule, ff.FireAt
	if rule != nil {
		if today := reminders.TodayInstead(*rule, localStart, fireAt, now, tz); today != nil {
			s = *today
			occ, _ := reminders.OccurrencesAfter(*today, now-1, 1)
			fireAt = occ[0]
		}
	}
	echo, err = reminders.FormatEcho(s, fireAt, now)
	if err != nil {
		return 0, "", "", err
	}
	if s.RRule != nil {
		start, err := reminders.FormatSeriesStart(s, fireAt, now)
		if err != nil {
			return 0, "", "", err
		}
		echo += ", " + start
	}
	return fireAt, reminders.UTCToISO(fireAt, tz), echo, nil
}

func TestGoldenTodayInstead(t *testing.T) {
	g := loadTimeGolden(t)
	for _, c := range g.Parses {
		fireAt, iso, echo, err := finishParse(c.Model.LocalDateTime, c.Model.RRule, c.TZ, c.Now)
		r := c.Result
		if !r.OK {
			if err == nil || err.Error() != r.Error {
				t.Errorf("finishParse(%+v, %s) error %v, want %q", c.Model, c.TZ, err, r.Error)
			}
			continue
		}
		if err != nil || fireAt != r.FireAt || iso != r.FireAtISO || echo != r.Echo {
			t.Errorf("finishParse(%+v, %s, %d) = %d %s %q %v, want %d %s %q", c.Model, c.TZ, c.Now, fireAt, iso, echo, err, r.FireAt, r.FireAtISO, r.Echo)
		}
	}
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isos(ms []int64, tz string) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = reminders.UTCToISO(m, tz)
	}
	return strings.Join(out, " ")
}
