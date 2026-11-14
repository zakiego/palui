// Package reminders ports packages/core/src/reminders. M2 ports the wall-clock
// recurrence (time.ts) and the schedule signal (signal.ts); M4 adds the service,
// parser, scheduler and actions here.
//
// A schedule is { rrule, tz, localStart }:
//
//	rrule       RRULE body without DTSTART ("FREQ=WEEKLY;BYDAY=MO"), or nil for one-off
//	tz          IANA zone
//	localStart  ISO local datetime without offset: the series anchor and time of day
//
// rrule runs in floating mode: local wall time is treated as if it were UTC, so
// "every Monday 08:00" stays 08:00 across DST changes. Each floating result is
// then placed in the zone here, never with rrule's TZID support.
//
//	DST gap (02:30 that doesn't exist): shifted forward, luxon's default (03:30).
//	DST overlap (01:30 that happens twice): the earlier instant (the first offset).
package reminders

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/teambition/rrule-go"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

type Schedule struct {
	RRule      *string `json:"rrule"`
	TZ         string  `json:"tz"`
	LocalStart string  `json:"localStart"`
}

type LocalParts struct {
	Year   int `json:"year"`
	Month  int `json:"month"`
	Day    int `json:"day"`
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
	Second int `json:"second"`
}

// TimeError carries a message meant for the owner.
type TimeError struct{ Msg string }

func (e *TimeError) Error() string { return e.Msg }

func timeErr(format string, a ...any) error { return &TimeError{fmt.Sprintf(format, a...)} }

const (
	hourMs = int64(3600_000)
	dayMs  = 24 * hourMs
	// Most occurrences rrule may produce per query window.
	maxResults = 2000
)

var (
	WeekdaysID = []string{"Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu", "Minggu"}
	MonthsID   = []string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}
	rruleDays  = []string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}
)

// --- Zones and local datetimes ---------------------------------------------------------

func IsValidZone(tz string) bool { return zone.Valid(tz) }

func AssertZone(tz string) error {
	if !IsValidZone(tz) {
		return timeErr("Unknown time zone %q. Use an IANA name like Asia/Makassar.", tz)
	}
	return nil
}

func loc(tz string) *time.Location { return zone.MustLoad(tz) }

var localRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2})(?::(\d{2})(?:\.\d+)?)?)?$`)

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// ParseLocal parses "2026-11-11T09:00", "2026-11-11T09:00:00" or a bare date
// (midnight). Offsets and "Z" are rejected.
func ParseLocal(iso string) (LocalParts, error) {
	m := localRe.FindStringSubmatch(strx.Trim(iso))
	if m == nil {
		return LocalParts{}, timeErr("%s isn't a local date-time. Use YYYY-MM-DDTHH:mm without an offset.", jsQuote(iso))
	}
	p := LocalParts{atoi(m[1]), atoi(m[2]), atoi(m[3]), atoi(m[4]), atoi(m[5]), atoi(m[6])}
	// luxon's fromObject in UTC: every unit in range, the day real for the month.
	if p.Month < 1 || p.Month > 12 || p.Day < 1 || p.Day > daysIn(p.Year, p.Month) || p.Hour > 23 || p.Minute > 59 || p.Second > 59 {
		return LocalParts{}, timeErr("%s isn't a real date.", jsQuote(iso))
	}
	return p, nil
}

// jsQuote is the TS template `"${s}"`: quotes around the raw text.
func jsQuote(s string) string { return `"` + s + `"` }

func daysIn(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func pad(n, w int) string {
	s := strconv.Itoa(n)
	for len(s) < w {
		s = "0" + s
	}
	return s
}

func FormatLocal(p LocalParts) string {
	return fmt.Sprintf("%s-%s-%sT%s:%s:%s", pad(p.Year, 4), pad(p.Month, 2), pad(p.Day, 2), pad(p.Hour, 2), pad(p.Minute, 2), pad(p.Second, 2))
}

func NormalizeLocal(iso string) (string, error) {
	p, err := ParseLocal(iso)
	if err != nil {
		return "", err
	}
	return FormatLocal(p), nil
}

func toFloating(p LocalParts) int64 {
	return time.Date(p.Year, time.Month(p.Month), p.Day, p.Hour, p.Minute, p.Second, 0, time.UTC).UnixMilli()
}

func fromFloating(t time.Time) LocalParts {
	t = t.UTC()
	return LocalParts{t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()}
}

// LocalToUTC turns a wall time in tz into Unix ms. See the package doc for the
// gap and overlap rules.
func LocalToUTC(local string, tz string) (int64, error) {
	if err := AssertZone(tz); err != nil {
		return 0, err
	}
	p, err := ParseLocal(local)
	if err != nil {
		return 0, err
	}
	return localPartsToUTC(p, tz), nil
}

// LocalPartsToUTC is LocalToUTC for parts already parsed.
func LocalPartsToUTC(p LocalParts, tz string) (int64, error) {
	if err := AssertZone(tz); err != nil {
		return 0, err
	}
	return localPartsToUTC(p, tz), nil
}

func localPartsToUTC(p LocalParts, tz string) int64 {
	l := loc(tz)
	asUTC := toFloating(p)
	// Every offset in effect within a day of this wall time is a candidate; keep
	// the ones that really map back to it and take the earliest instant.
	best := int64(0)
	found := false
	for _, probe := range []int64{asUTC - dayMs, asUTC, asUTC + dayMs} {
		off := zone.OffsetMin(l, probe)
		t := asUTC - int64(off)*60_000
		if zone.OffsetMin(l, t) == off && (!found || t < best) {
			best, found = t, true
		}
	}
	if found {
		return best
	}
	// The wall time falls in a DST gap: luxon shifts it forward by the gap.
	return zone.FromParts(zone.Parts{Year: p.Year, Month: p.Month, Day: p.Day, Hour: p.Hour, Minute: p.Minute, Second: p.Second}, l)
}

func UTCToLocalParts(ms int64, tz string) LocalParts {
	t := time.UnixMilli(ms).In(loc(tz))
	return LocalParts{t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()}
}

func UTCToLocal(ms int64, tz string) string { return FormatLocal(UTCToLocalParts(ms, tz)) }

// UTCToISO is the ISO form with the zone's offset, e.g. "2026-11-11T09:00:00.000+08:00".
func UTCToISO(ms int64, tz string) string { return zone.ISO(ms, tz, loc(tz)) }

// --- RRULE ---------------------------------------------------------------------------

type rrulePart struct{ k, v string }

func splitRrule(body string) ([]rrulePart, error) {
	var out []rrulePart
	for _, raw := range strings.Split(body, ";") {
		part := strx.Trim(raw)
		if part == "" {
			continue
		}
		i := strings.Index(part, "=")
		if i < 1 {
			return nil, timeErr("Bad RRULE part %s.", jsQuote(part))
		}
		out = append(out, rrulePart{strings.ToUpper(part[:i]), strings.ToUpper(part[i+1:])})
	}
	return out, nil
}

var listKeys = map[string]bool{"BYDAY": true, "BYMONTH": true, "BYMONTHDAY": true, "BYHOUR": true, "BYMINUTE": true, "BYSECOND": true, "BYSETPOS": true, "BYYEARDAY": true, "BYWEEKNO": true}

var bydayRank = regexp.MustCompile(`^([+-]?\d+)?([A-Z]{2})$`)

func dayIndex(code string) int {
	for i, d := range rruleDays {
		if d == code {
			return i
		}
	}
	return -1
}

func sortListValue(key, value string) string {
	if !listKeys[key] {
		return value
	}
	var items []string
	for _, s := range strings.Split(value, ",") {
		if s != "" {
			items = append(items, s)
		}
	}
	if key == "BYDAY" {
		rank := func(v string) float64 {
			m := bydayRank.FindStringSubmatch(v)
			code, n := "MO", 0.0
			if m != nil {
				code = m[2]
				if m[1] != "" {
					n = strx.Number(m[1])
				}
			}
			return float64(dayIndex(code))*1000 + n
		}
		seen := map[string]bool{}
		var uniq []string
		for _, v := range items {
			v = strings.TrimPrefix(v, "+")
			if !seen[v] {
				seen[v] = true
				uniq = append(uniq, v)
			}
		}
		sort.SliceStable(uniq, func(i, j int) bool { return rank(uniq[i]) < rank(uniq[j]) })
		return strings.Join(uniq, ",")
	}
	seen := map[string]bool{}
	var nums []float64
	for _, v := range items {
		n := strx.Number(v)
		k := jsonx.NumberString(n)
		if !seen[k] {
			seen[k] = true
			nums = append(nums, n)
		}
	}
	sort.SliceStable(nums, func(i, j int) bool { return nums[i] < nums[j] })
	out := make([]string, len(nums))
	for i, n := range nums {
		out[i] = jsonx.NumberString(n)
	}
	return strings.Join(out, ",")
}

var keyOrder = []string{"FREQ", "INTERVAL", "BYMONTH", "BYWEEKNO", "BYYEARDAY", "BYMONTHDAY", "BYDAY", "BYHOUR", "BYMINUTE", "BYSECOND", "BYSETPOS", "COUNT", "UNTIL", "WKST"}

func keyRank(k string) int {
	for i, x := range keyOrder {
		if x == k {
			return i
		}
	}
	return -1
}

const (
	minGapMs   = int64(5 * 60_000)
	probeCount = 50
)

// probeStart is Date.UTC(9988, 0, 1): 12 years before rrule's end of time, so
// a rule that never fires is found out in a few milliseconds.
var probeStart = time.Date(9988, 1, 1, 0, 0, 0, 0, time.UTC)

// ruleOptions is what rrule.js parseString yields for a body, in rrule-go terms.
type ruleOptions struct {
	opt      rrule.ROption
	hasFreq  bool
	freq     rrule.Frequency
	interval *int
	count    int
}

var frequencies = map[string]rrule.Frequency{"YEARLY": rrule.YEARLY, "MONTHLY": rrule.MONTHLY, "WEEKLY": rrule.WEEKLY, "DAILY": rrule.DAILY, "HOURLY": rrule.HOURLY, "MINUTELY": rrule.MINUTELY, "SECONDLY": rrule.SECONDLY}

var weekdays = []rrule.Weekday{rrule.MO, rrule.TU, rrule.WE, rrule.TH, rrule.FR, rrule.SA, rrule.SU}

var nthDay = regexp.MustCompile(`^([+-]?\d{1,2})([A-Z]{2})$`)
var untilRe = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})(T(\d{2})(\d{2})(\d{2})Z?)?$`)

var intRe = regexp.MustCompile(`^[+-]?\d+$`)

func parseInts(key, value string) ([]int, error) {
	var out []int
	for _, s := range strings.Split(value, ",") {
		if !intRe.MatchString(s) {
			return nil, fmt.Errorf("Invalid %s value: %s", strings.ToLower(key), s)
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// parseRuleOptions is rrule.js RRule.parseString for a validated body (known
// keys only). Values rrule.js would keep as junk strings are errors here.
//
// lenient is the first parse of the raw body, where rrule.js maps an unknown
// two-letter day ("we" before uppercasing) to undefined without throwing.
func parseRuleOptions(s string, lenient bool) (ruleOptions, error) {
	var o ruleOptions
	o.opt.Wkst = rrule.MO
	for _, attr := range strings.Split(s, ";") {
		kv := strings.Split(attr, "=")
		key := strings.ToUpper(kv[0])
		value := ""
		if len(kv) > 1 {
			value = kv[1]
		}
		switch key {
		case "FREQ":
			f, ok := frequencies[strings.ToUpper(value)]
			o.hasFreq, o.freq = ok, f
		case "WKST":
			if i := dayIndex(strings.ToUpper(value)); i >= 0 {
				o.opt.Wkst = weekdays[i]
			}
		case "BYDAY":
			for _, day := range strings.Split(value, ",") {
				if len(day) == 2 {
					i := dayIndex(day)
					if i < 0 && lenient {
						continue
					}
					if i < 0 {
						return o, fmt.Errorf("Invalid weekday string: %s", day)
					}
					o.opt.Byweekday = append(o.opt.Byweekday, weekdays[i])
					continue
				}
				m := nthDay.FindStringSubmatch(day)
				if m == nil {
					return o, fmt.Errorf("Invalid weekday string: %s", day)
				}
				i := dayIndex(m[2])
				if i < 0 {
					return o, fmt.Errorf("Cannot read properties of undefined (reading 'weekday')")
				}
				n, _ := strconv.Atoi(m[1])
				o.opt.Byweekday = append(o.opt.Byweekday, weekdays[i].Nth(n))
			}
		case "UNTIL":
			m := untilRe.FindStringSubmatch(value)
			if m == nil {
				return o, fmt.Errorf("Invalid UNTIL value: %s", value)
			}
			o.opt.Until = time.Date(atoi(m[1]), time.Month(atoi(m[2])), atoi(m[3]), atoi(m[5]), atoi(m[6]), atoi(m[7]), 0, time.UTC)
		case "":
			return o, fmt.Errorf("Unknown RRULE property ''")
		default:
			nums, err := parseInts(key, value)
			if err != nil {
				return o, err
			}
			switch key {
			case "COUNT":
				o.count = nums[len(nums)-1]
				o.opt.Count = o.count
			case "INTERVAL":
				n := nums[len(nums)-1]
				o.interval = &n
				o.opt.Interval = n
			case "BYSETPOS":
				o.opt.Bysetpos = nums
			case "BYMONTH":
				o.opt.Bymonth = nums
			case "BYMONTHDAY":
				o.opt.Bymonthday = nums
			case "BYYEARDAY":
				o.opt.Byyearday = nums
			case "BYWEEKNO":
				o.opt.Byweekno = nums
			case "BYHOUR":
				o.opt.Byhour = nums
			case "BYMINUTE":
				o.opt.Byminute = nums
			case "BYSECOND":
				o.opt.Bysecond = nums
			default:
				return o, fmt.Errorf("Unknown RRULE property '%s'", key)
			}
		}
	}
	o.opt.Freq = o.freq
	return o, nil
}

// errNeverFires marks a rule rrule-go refuses for an out-of-range BY value;
// rrule.js accepts those and simply never matches.
type boundsError struct{ error }

// newRule is `new RRule({ ...opts, dtstart, tzid: null })`.
func newRule(o ruleOptions, dtstart time.Time, withCount bool) (*rrule.RRule, error) {
	opt := o.opt
	opt.Dtstart = dtstart
	if !withCount {
		opt.Count = 0
		opt.Until = time.Time{}
	}
	for _, v := range opt.Bysetpos {
		if v == 0 || v < -366 || v > 366 {
			return nil, fmt.Errorf("bysetpos must be between 1 and 366, or between -366 and -1")
		}
	}
	r, err := rrule.NewRRule(opt)
	if err != nil {
		return nil, boundsError{err}
	}
	return r, nil
}

// each walks the occurrences in [from, to] (inclusive) in order, stopping when
// fn returns false. It always iterates from DTSTART, like rrule.js.
func each(r *rrule.RRule, from, to time.Time, fn func(time.Time) bool) {
	next := r.Iterator()
	for {
		d, ok := next()
		if !ok || d.After(to) {
			return
		}
		if d.Before(from) {
			continue
		}
		if !fn(d) {
			return
		}
	}
}

// probeRrule rejects rules that fire more often than every 5 minutes and rules
// that never fire (or less than once in 12 years). COUNT and UNTIL are ignored.
func probeRrule(o ruleOptions, s string) error {
	r, err := newRule(o, probeStart, false)
	if err != nil {
		var be boundsError
		if asBounds(err, &be) {
			return timeErr("RRULE %s never fires.", jsQuote(s))
		}
		return err
	}
	var dates []time.Time
	next := r.Iterator()
	for len(dates) < probeCount {
		d, ok := next()
		if !ok {
			break
		}
		dates = append(dates, d)
	}
	if len(dates) == 0 {
		return timeErr("RRULE %s never fires.", jsQuote(s))
	}
	for i := 1; i < len(dates); i++ {
		if dates[i].Sub(dates[i-1]).Milliseconds() < minGapMs {
			return timeErr("Reminders can repeat at most every 5 minutes.")
		}
	}
	return nil
}

func asBounds(err error, target *boundsError) bool {
	be, ok := err.(boundsError)
	if ok {
		*target = be
	}
	return ok
}

// normalizeRrule runs on every occurrence query, and the probe isn't free.
var (
	normMu     sync.Mutex
	normalized = map[string]normResult{}
)

type normResult struct {
	s   string
	err error
}

// NormalizeRrule validates an RRULE body and returns its canonical form: known
// keys only, keys in a fixed order, list values sorted, INTERVAL=1 and WKST=MO
// dropped. Errors are *TimeError with a readable message (a plain error only
// for what rrule.js itself throws past the TS checks).
func NormalizeRrule(body string) (string, error) {
	normMu.Lock()
	hit, ok := normalized[body]
	normMu.Unlock()
	if ok {
		return hit.s, hit.err
	}
	out, err := normalizeRruleUncached(body)
	if err == nil {
		// The TS cache only keeps successes (a throw skips the set).
		normMu.Lock()
		if len(normalized) >= 1000 {
			normalized = map[string]normResult{}
		}
		normalized[body] = normResult{out, nil}
		normMu.Unlock()
	}
	return out, err
}

var (
	rrulePrefix = regexp.MustCompile(`(?i)^RRULE:`)
	forbidden   = regexp.MustCompile(`(?i)DTSTART|TZID|\n`)
)

func normalizeRruleUncached(body string) (string, error) {
	s := rrulePrefix.ReplaceAllString(strx.Trim(body), "")
	if s == "" {
		return "", timeErr("Empty RRULE.")
	}
	if forbidden.MatchString(s) {
		return "", timeErr("Give the RRULE body only, without DTSTART or TZID.")
	}
	parts, err := splitRrule(s)
	if err != nil {
		return "", err
	}
	keys := map[string]bool{}
	for _, p := range parts {
		if keyRank(p.k) < 0 {
			return "", timeErr("Unsupported RRULE key %s.", p.k)
		}
		if keys[p.k] {
			return "", timeErr("RRULE key %s appears twice.", p.k)
		}
		keys[p.k] = true
	}
	opts, err := parseRuleOptions(s, true)
	if err != nil {
		return "", timeErr("Bad RRULE %s: %s", jsQuote(s), err.Error())
	}
	if !opts.hasFreq {
		return "", timeErr("RRULE %s needs a valid FREQ.", jsQuote(s))
	}
	if opts.freq == rrule.SECONDLY {
		return "", timeErr("Reminders can't repeat every few seconds.")
	}
	interval := 1
	if opts.interval != nil {
		interval = *opts.interval
	}
	if opts.freq == rrule.MINUTELY && interval < 5 {
		return "", timeErr("Reminders can repeat at most every 5 minutes.")
	}
	if opts.interval != nil && *opts.interval < 1 {
		return "", timeErr("INTERVAL must be at least 1.")
	}
	var kept []rrulePart
	for _, p := range parts {
		if (p.k == "INTERVAL" && strx.Number(p.v) == 1) || (p.k == "WKST" && p.v == "MO") {
			continue
		}
		kept = append(kept, rrulePart{p.k, sortListValue(p.k, p.v)})
	}
	sort.SliceStable(kept, func(i, j int) bool { return keyRank(kept[i].k) < keyRank(kept[j].k) })
	items := make([]string, len(kept))
	for i, p := range kept {
		items[i] = p.k + "=" + p.v
	}
	canonical := strings.Join(items, ";")
	copts, err := parseRuleOptions(canonical, false)
	if err != nil {
		return "", timeErr("Bad RRULE %s: %s", jsQuote(s), err.Error())
	}
	if err := probeRrule(copts, s); err != nil {
		return "", err
	}
	return canonical, nil
}

func rruleParts(body string) map[string]string {
	parts, _ := splitRrule(body)
	out := map[string]string{}
	for _, p := range parts {
		out[p.k] = p.v
	}
	return out
}

// fixedPeriodMs: rules whose period is a fixed span of floating time.
var fixedPeriodMs = map[rrule.Frequency]int64{rrule.MINUTELY: 60_000, rrule.HOURLY: hourMs, rrule.DAILY: dayMs, rrule.WEEKLY: 7 * dayMs}

// ruleFor builds the floating rule. rrule iterates from DTSTART on every query,
// so for rules with a fixed period and no COUNT, DTSTART moves forward by whole
// periods to near windowStart (nil: no window).
func ruleFor(rr string, localStart string, windowStart *int64) (*rrule.RRule, error) {
	norm, err := NormalizeRrule(rr)
	if err != nil {
		return nil, err
	}
	opts, err := parseRuleOptions(norm, false)
	if err != nil {
		return nil, timeErr("Bad RRULE %s: %s", jsQuote(norm), err.Error())
	}
	p, err := ParseLocal(localStart)
	if err != nil {
		return nil, err
	}
	dtstart := toFloating(p)
	if unit, ok := fixedPeriodMs[opts.freq]; ok && windowStart != nil && opts.count == 0 {
		interval := int64(1)
		if opts.interval != nil {
			interval = int64(*opts.interval)
		}
		period := unit * interval
		target := *windowStart - 2*dayMs - period
		if target > dtstart {
			dtstart += int64(math.Floor(float64(target-dtstart)/float64(period))) * period
		}
	}
	// UNTIL is read as floating local time too (it went through the same parser).
	r, err := newRule(opts, time.UnixMilli(dtstart).UTC(), true)
	if err != nil {
		var be boundsError
		if asBounds(err, &be) {
			return nil, timeErr("RRULE %s never fires.", jsQuote(rr))
		}
		return nil, err
	}
	return r, nil
}

// OccurrencesAfter is the next n fire instants strictly after afterMs, in order.
func OccurrencesAfter(s Schedule, afterMs int64, n int) ([]int64, error) {
	if err := AssertZone(s.TZ); err != nil {
		return nil, err
	}
	if s.RRule == nil || *s.RRule == "" {
		t, err := LocalToUTC(s.LocalStart, s.TZ)
		if err != nil {
			return nil, err
		}
		if t > afterMs && n > 0 {
			return []int64{t}, nil
		}
		return []int64{}, nil
	}
	out := []int64{}
	// Start a little before `after` in wall time: around DST changes, a wall time
	// before the local "now" may still be in the future.
	from := toFloating(UTCToLocalParts(afterMs, s.TZ)) - 26*hourMs
	rule, err := ruleFor(*s.RRule, s.LocalStart, &from)
	if err != nil {
		return nil, err
	}
	for _, span := range []int64{3 * dayMs, 400 * dayMs, 40 * 366 * dayMs} {
		to := from + span
		seen := 0
		each(rule, time.UnixMilli(from).UTC(), time.UnixMilli(to).UTC(), func(d time.Time) bool {
			seen++
			if seen > maxResults {
				return false
			}
			t := localPartsToUTC(fromFloating(d), s.TZ)
			if t > afterMs && (len(out) == 0 || t > out[len(out)-1]) {
				out = append(out, t)
			}
			return len(out) < n
		})
		if len(out) >= n || n <= 0 {
			return out[:max(n, 0)], nil
		}
		from = to
	}
	return out, nil
}

// NextOccurrence is the next fire instant strictly after afterMs; ok is false
// when the series is over.
func NextOccurrence(s Schedule, afterMs int64) (int64, bool, error) {
	out, err := OccurrencesAfter(s, afterMs, 1)
	if err != nil || len(out) == 0 {
		return 0, false, err
	}
	return out[0], true, nil
}

// LatestAtOrBefore is the latest occurrence in [fromMs, atMs], scanning forward
// from fromMs (itself an occurrence). After 10 000 steps it returns the last seen.
func LatestAtOrBefore(s Schedule, fromMs, atMs int64) (int64, error) {
	last := fromMs
	for i := 0; i < 100; i++ {
		batch, err := OccurrencesAfter(s, last, 100)
		if err != nil {
			return 0, err
		}
		for _, t := range batch {
			if t > atMs {
				return last, nil
			}
			last = t
		}
		if len(batch) < 100 {
			return last, nil
		}
	}
	return last, nil
}

// --- Formatting (Indonesian echo) ------------------------------------------------------

var zoneLabels = map[string]string{
	"Asia/Jakarta":   "WIB",
	"Asia/Pontianak": "WIB",
	"Asia/Makassar":  "WITA",
	"Asia/Jayapura":  "WIT",
}

func ZoneLabel(tz string, ms int64) string {
	if l, ok := zoneLabels[tz]; ok {
		return l
	}
	return zone.ShortName(ms, tz)
}

// FormatClock is "09.00".
func FormatClock(ms int64, tz string) string {
	return time.UnixMilli(ms).In(loc(tz)).Format("15.04")
}

// dayDiff is the number of calendar days from now's local date to ms's.
func dayDiff(ms int64, tz string, now int64) int {
	a := UTCToLocalParts(ms, tz)
	b := UTCToLocalParts(now, tz)
	da := time.Date(a.Year, time.Month(a.Month), a.Day, 0, 0, 0, 0, time.UTC)
	db := time.Date(b.Year, time.Month(b.Month), b.Day, 0, 0, 0, 0, time.UTC)
	return int(da.Sub(db).Hours() / 24)
}

func isoWeekday(t time.Time) int {
	w := int(t.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

// FormatDate is "Senin 16 Nov" (with the year when it isn't this year).
func FormatDate(ms int64, tz string, now int64) string {
	l := loc(tz)
	d := time.UnixMilli(ms).In(l)
	year := ""
	if d.Year() != time.UnixMilli(now).In(l).Year() {
		year = " " + strconv.Itoa(d.Year())
	}
	return fmt.Sprintf("%s %d %s%s", WeekdaysID[isoWeekday(d)-1], d.Day(), MonthsID[d.Month()-1], year)
}

// FormatWhen is "Hari ini, 14.30 WITA" / "Besok, Rabu 11 Nov, 09.00 WITA" /
// "Senin 16 Nov, 08.00 WITA".
func FormatWhen(ms int64, tz string, now int64) string {
	clock := FormatClock(ms, tz) + " " + ZoneLabel(tz, ms)
	switch dayDiff(ms, tz, now) {
	case 0:
		return "Hari ini, " + clock
	case 1:
		return "Besok, " + FormatDate(ms, tz, now) + ", " + clock
	}
	return FormatDate(ms, tz, now) + ", " + clock
}

func joinID(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " dan " + items[len(items)-1]
}

var ordinalsID = map[string]string{"1": "pertama", "2": "kedua", "3": "ketiga", "4": "keempat", "-1": "terakhir", "-2": "kedua terakhir"}

func dayName(code string) string {
	if i := dayIndex(code); i >= 0 {
		return WeekdaysID[i]
	}
	return code
}

func monthName(m int) string {
	if m >= 1 && m <= 12 {
		return MonthsID[m-1]
	}
	return "undefined"
}

// timesOnDay is the wall-clock times the series fires on the local day of dayMs.
func timesOnDay(s Schedule, dayMs_ int64) ([]string, error) {
	p := UTCToLocalParts(dayMs_, s.TZ)
	start := time.Date(p.Year, time.Month(p.Month), p.Day, 0, 0, 0, 0, time.UTC).UnixMilli()
	rule, err := ruleFor(*s.RRule, s.LocalStart, &start)
	if err != nil {
		return nil, err
	}
	var out []string
	each(rule, time.UnixMilli(start).UTC(), time.UnixMilli(start+dayMs-1).UTC(), func(d time.Time) bool {
		if len(out) >= 500 {
			return false
		}
		out = append(out, pad(d.Hour(), 2)+"."+pad(d.Minute(), 2))
		return true
	})
	return out, nil
}

var (
	plainDay = regexp.MustCompile(`^[A-Z]{2}$`)
	ordDay   = regexp.MustCompile(`^([+-]?\d+)([A-Z]{2})$`)
	untilYMD = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})`)
)

func numStr(s string) string { return jsonx.NumberString(strx.Number(s)) }

// DescribeRecurrence is "Tiap Senin, 08.00 WITA", "Tiap hari, 07.00 WITA",
// "Tiap Jumat terakhir, 17.00 WITA". s.RRule must be set.
func DescribeRecurrence(s Schedule, now int64) (string, error) {
	norm, err := NormalizeRrule(*s.RRule)
	if err != nil {
		return "", err
	}
	p := rruleParts(norm)
	interval := 1.0
	if v, ok := p["INTERVAL"]; ok {
		interval = strx.Number(v)
	}
	intervalStr := jsonx.NumberString(interval)
	start, err := ParseLocal(s.LocalStart)
	if err != nil {
		return "", err
	}
	startDt := time.Date(start.Year, time.Month(start.Month), start.Day, start.Hour, start.Minute, start.Second, 0, time.UTC)
	next, ok, err := NextOccurrence(s, now)
	if err != nil {
		return "", err
	}
	if !ok {
		next, err = LocalPartsToUTC(start, s.TZ)
		if err != nil {
			return "", err
		}
	}
	var days []string
	if v, ok := p["BYDAY"]; ok && v != "" {
		days = strings.Split(v, ",")
	}
	var plainDays []string
	for _, d := range days {
		if plainDay.MatchString(d) {
			plainDays = append(plainDays, d)
		}
	}
	has := func(list []string, x string) bool {
		for _, v := range list {
			if v == x {
				return true
			}
		}
		return false
	}
	isWeekdays := len(plainDays) == 5
	for _, d := range []string{"MO", "TU", "WE", "TH", "FR"} {
		isWeekdays = isWeekdays && has(plainDays, d)
	}
	isWeekend := len(plainDays) == 2 && has(plainDays, "SA") && has(plainDays, "SU")
	var namedDays string
	switch {
	case isWeekdays:
		namedDays = "hari kerja"
	case isWeekend:
		namedDays = "akhir pekan"
	default:
		names := make([]string, len(plainDays))
		for i, d := range plainDays {
			names[i] = dayName(d)
		}
		namedDays = joinID(names)
	}

	var head string
	withTime := true
	switch p["FREQ"] {
	case "DAILY":
		switch {
		case len(days) > 0:
			head = "Tiap " + namedDays
		case interval > 1:
			head = "Tiap " + intervalStr + " hari"
		default:
			head = "Tiap hari"
		}
	case "WEEKLY":
		list := namedDays
		if len(days) == 0 {
			list = WeekdaysID[isoWeekday(startDt)-1]
		}
		if interval > 1 {
			head = "Tiap " + intervalStr + " minggu, " + list
		} else {
			head = "Tiap " + list
		}
	case "MONTHLY":
		every := ""
		if interval > 1 {
			every = " tiap " + intervalStr + " bulan"
		}
		var ord []string
		if len(days) == 1 {
			ord = ordDay.FindStringSubmatch(days[0])
		}
		setpos := ""
		if p["BYSETPOS"] != "" && len(days) == 1 && ord == nil {
			setpos = p["BYSETPOS"]
		}
		var ordName string
		if ord != nil {
			ordName = ordinalsID[numStr(ord[1])]
		}
		switch {
		case ord != nil && ordName != "":
			head = "Tiap " + dayName(ord[2]) + " " + ordName + every
		case setpos != "" && ordinalsID[setpos] != "":
			head = "Tiap " + dayName(days[0]) + " " + ordinalsID[setpos] + every
		case p["BYMONTHDAY"] == "-1":
			head = "Tiap akhir bulan" + every
		case p["BYMONTHDAY"] != "":
			head = "Tiap tanggal " + joinID(strings.Split(p["BYMONTHDAY"], ",")) + every
		case len(days) > 0:
			head = "Tiap " + namedDays + every
		default:
			head = "Tiap tanggal " + strconv.Itoa(start.Day) + every
		}
	case "YEARLY":
		month := start.Month
		if v := p["BYMONTH"]; v != "" {
			month = int(strx.Number(strings.Split(v, ",")[0]))
		}
		day := strconv.Itoa(start.Day)
		if v := p["BYMONTHDAY"]; v != "" {
			day = strings.Split(v, ",")[0]
		}
		suffix := ""
		if interval > 1 {
			suffix = " (tiap " + intervalStr + " tahun)"
		}
		head = "Tiap " + day + " " + monthName(month) + suffix
	case "HOURLY":
		if interval > 1 {
			head = "Tiap " + intervalStr + " jam"
		} else {
			head = "Tiap jam"
		}
		withTime = false
	case "MINUTELY":
		head = "Tiap " + intervalStr + " menit"
		withTime = false
	default:
		head = "Berulang"
	}

	tail := ""
	if v := p["COUNT"]; v != "" {
		tail += ", " + v + " kali"
	}
	if v := p["UNTIL"]; v != "" {
		if u := untilYMD.FindStringSubmatch(v); u != nil {
			tail += fmt.Sprintf(", sampai %s %s %s", numStr(u[3]), monthName(int(strx.Number(u[2]))), u[1])
		}
	}
	if !withTime {
		return head + tail, nil
	}

	times, err := timesOnDay(s, next)
	if err != nil {
		return "", err
	}
	var clock string
	switch {
	case len(times) == 0:
		clock = pad(start.Hour, 2) + "." + pad(start.Minute, 2)
	case len(times) <= 3:
		clock = joinID(times)
	default:
		mins := make([]int, len(times))
		for i, t := range times {
			mins[i] = atoi(t[:2])*60 + atoi(t[3:])
		}
		gap := mins[1] - mins[0]
		even := true
		for i := 1; i < len(mins); i++ {
			even = even && mins[i]-mins[i-1] == gap
		}
		prefix := ""
		if even {
			if gap >= 60 && gap%60 == 0 {
				prefix = fmt.Sprintf("tiap %d jam ", gap/60)
			} else {
				prefix = fmt.Sprintf("tiap %d menit ", gap)
			}
		}
		clock = prefix + times[0] + " sampai " + times[len(times)-1]
	}
	return head + ", " + clock + " " + ZoneLabel(s.TZ, next) + tail, nil
}

// FormatEcho is the echo after a reminder is set: recurring rules are described,
// one-offs get their date and time relative to now.
func FormatEcho(s Schedule, fireAt, now int64) (string, error) {
	if s.RRule != nil && *s.RRule != "" {
		return DescribeRecurrence(s, now)
	}
	return FormatWhen(fireAt, s.TZ, now), nil
}

// FormatSeriesStart is "mulai Senin 16 Nov", appended to a series echo.
// Hourly and minutely series carry the clock too.
func FormatSeriesStart(s Schedule, fireAt, now int64) (string, error) {
	norm, err := NormalizeRrule(*s.RRule)
	if err != nil {
		return "", err
	}
	freq := rruleParts(norm)["FREQ"]
	clock := ""
	if freq == "HOURLY" || freq == "MINUTELY" {
		clock = " " + FormatClock(fireAt, s.TZ) + " " + ZoneLabel(s.TZ, fireAt)
	}
	switch dayDiff(fireAt, s.TZ, now) {
	case 0:
		return "mulai hari ini" + clock, nil
	case 1:
		return "mulai besok, " + FormatDate(fireAt, s.TZ, now) + clock, nil
	}
	return "mulai " + FormatDate(fireAt, s.TZ, now) + clock, nil
}

// --- First fire (shared by the parser and the service) ---------------------------------

// MaxLeadMs: a first fire further out than this is refused as a likely misparse.
const MaxLeadMs = 731 * dayMs

type FirstFire struct {
	Schedule Schedule `json:"schedule"`
	FireAt   int64    `json:"fireAt"`
	Rolled   bool     `json:"rolled"`
}

// ResolveFirstFire is the first fire of a new or edited schedule (localStart
// already normalized):
//   - a one-off earlier today moves to the same time tomorrow (Rolled)
//   - a one-off in the past, or a series with no run from now on, is refused
//   - a first fire more than about 2 years away is refused
func ResolveFirstFire(s Schedule, now int64) (FirstFire, error) {
	schedule := s
	fireAt, ok, err := NextOccurrence(s, now-1)
	if err != nil {
		return FirstFire{}, err
	}
	rolled := false
	recurring := s.RRule != nil && *s.RRule != ""
	if !ok && !recurring && strx.Slice16(s.LocalStart, 10) == UTCToLocal(now, s.TZ)[:10] {
		p, err := ParseLocal(s.LocalStart)
		if err != nil {
			return FirstFire{}, err
		}
		d := time.Date(p.Year, time.Month(p.Month), p.Day+1, p.Hour, p.Minute, p.Second, 0, time.UTC)
		schedule.LocalStart = FormatLocal(LocalParts{d.Year(), int(d.Month()), d.Day(), d.Hour(), d.Minute(), d.Second()})
		fireAt, ok, err = NextOccurrence(schedule, now-1)
		if err != nil {
			return FirstFire{}, err
		}
		rolled = true
	}
	if !ok {
		if recurring {
			return FirstFire{}, timeErr("That repeat rule has no occurrence from now on.")
		}
		at, err := LocalToUTC(s.LocalStart, s.TZ)
		if err != nil {
			return FirstFire{}, err
		}
		return FirstFire{}, timeErr("That time has already passed (%s). Pick a time in the future.", FormatWhen(at, s.TZ, now))
	}
	if fireAt-now > MaxLeadMs {
		return FirstFire{}, timeErr("That's more than 2 years away (%s). Pick a nearer date.", FormatWhen(fireAt, s.TZ, now))
	}
	return FirstFire{Schedule: schedule, FireAt: fireAt, Rolled: rolled}, nil
}

// TomorrowAt is tomorrow hour:minute wall time in tz (the "Besok 09.00" snooze).
func TomorrowAt(now int64, tz string, hour, minute int) int64 {
	p := UTCToLocalParts(now, tz)
	d := time.Date(p.Year, time.Month(p.Month), p.Day+1, 0, 0, 0, 0, time.UTC)
	return localPartsToUTC(LocalParts{d.Year(), int(d.Month()), d.Day(), hour, minute, 0}, tz)
}

var (
	todayFreq  = regexp.MustCompile(`^FREQ=(DAILY|WEEKLY|MONTHLY)(;|$)`)
	todayBlock = regexp.MustCompile(`INTERVAL=|COUNT=|BYHOUR=|BYMINUTE=|BYSETPOS=`)
	monthlyBy  = regexp.MustCompile(`BYDAY=|BYMONTHDAY=`)
)

// TodayInstead is todayInstead from reminders/parse.ts (M4 calls it from the
// parser): models sometimes start a series at the next matching day and skip a
// run still ahead today. The start moves to today only when the model's first
// fire is exactly the next matching day after today. nil means keep it.
func TodayInstead(rr, localStart string, fireAt, now int64, tz string) *Schedule {
	if !todayFreq.MatchString(rr) || todayBlock.MatchString(rr) {
		return nil
	}
	if strings.HasPrefix(rr, "FREQ=WEEKLY") && !strings.Contains(rr, "BYDAY=") {
		return nil
	}
	if strings.HasPrefix(rr, "FREQ=MONTHLY") && !monthlyBy.MatchString(rr) {
		return nil
	}
	todayDate := UTCToLocal(now, tz)[:10]
	rest := ""
	if len(localStart) > 10 {
		rest = localStart[10:]
	}
	today := Schedule{RRule: &rr, TZ: tz, LocalStart: todayDate + rest}
	occ, err := OccurrencesAfter(today, now-1, 2)
	if err != nil || len(occ) < 2 {
		return nil
	}
	if UTCToLocal(occ[0], tz)[:10] != todayDate || occ[1] != fireAt {
		return nil
	}
	return &today
}
