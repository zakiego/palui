package flows

// Value helpers shared by expressions and templates (values.ts): paths into
// data, dates in a zone, truthiness. Pure, and never fails on odd data: a
// value that doesn't fit is null.

import (
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

// Scope is what templates and expressions read: root name -> value.
type Scope map[string]any

var digitsRe = regexp.MustCompile(`^\d+$`)

// GetIn is getIn: segs are string keys or float64/int indices.
func GetIn(value any, segs []any) any {
	cur := value
	for _, s := range segs {
		switch c := cur.(type) {
		case nil, undefT:
			return nil
		case []any:
			idx := -1.0
			isInt := false
			switch k := s.(type) {
			case float64:
				idx, isInt = k, k == math.Trunc(k)
			case int:
				idx, isInt = float64(k), true
			case string:
				if digitsRe.MatchString(k) {
					n, _ := strconv.ParseFloat(k, 64)
					idx, isInt = n, true
				}
			}
			if !isInt || idx < 0 || idx >= float64(len(c)) {
				cur = undef
			} else {
				cur = c[int(idx)]
			}
		case *jsonx.Object:
			key := segString(s)
			v, ok := c.Get(key)
			if !ok {
				cur = nil
			} else {
				cur = v
			}
		default:
			return nil
		}
	}
	if isUndef(cur) {
		return nil
	}
	return cur
}

func segString(s any) string {
	switch k := s.(type) {
	case string:
		return k
	case float64:
		return jsonx.NumberString(k)
	case int:
		return strconv.Itoa(k)
	}
	return ""
}

// IsEmpty is isEmpty.
func IsEmpty(v any) bool {
	switch t := v.(type) {
	case nil, undefT:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case *jsonx.Object:
		return t.Len() == 0
	}
	return false
}

// Truthy is the flow truthiness: false, 0, NaN and empty values are false.
func Truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		if !t {
			return false
		}
	case float64:
		if t == 0 || math.IsNaN(t) {
			return false
		}
	}
	return !IsEmpty(v)
}

var (
	dateRe    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	clockRe   = regexp.MustCompile(`^(\d{1,2})[:.](\d{2})(?::\d{2})?$`)
	localDTRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?$`)
	absDTRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})$`)
	absPartRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?(Z|([+-])(\d{2}):?(\d{2}))$`)
)

// dateParse is Date.parse for strings absDTRe accepts. ok false is NaN.
func dateParse(s string) (int64, bool) {
	m := absPartRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n := func(x string) int { v, _ := strconv.Atoi(x); return v }
	y, mo, d, h, mi, sec := n(m[1]), n(m[2]), n(m[3]), n(m[4]), n(m[5]), n(m[6])
	ms := 0
	if m[7] != "" {
		frac := (m[7] + "00")[:3]
		ms = n(frac)
	}
	if mo < 1 || mo > 12 || d < 1 || d > time.Date(y, time.Month(mo)+1, 0, 0, 0, 0, 0, time.UTC).Day() {
		return 0, false
	}
	if mi > 59 || sec > 59 || h > 24 || (h == 24 && (mi != 0 || sec != 0 || ms != 0)) {
		return 0, false
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, ms*1e6, time.UTC).UnixMilli()
	if m[8] != "Z" {
		oh, om := n(m[10]), n(m[11])
		if oh > 23 || om > 59 {
			return 0, false
		}
		off := int64(oh*60+om) * 60_000
		if m[9] == "+" {
			t -= off
		} else {
			t += off
		}
	}
	return t, true
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// ToMs is toMs: an ISO date (start of that day in tz), a local or absolute
// datetime, a clock time (today in tz) or epoch ms/s -> UTC ms.
func ToMs(v any, tz string, now int64) (int64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return 0, false
		}
		if t > 1e11 {
			return int64(mathFloor(t + 0.5)), true
		}
		if t > 1e9 {
			return int64(mathFloor(t*1000 + 0.5)), true
		}
		return 0, false
	case string:
		s := strx.Trim(t)
		if dateRe.MatchString(s) {
			ms, err := reminders.LocalToUTC(s+"T00:00", tz)
			return ms, err == nil
		}
		if c := clockRe.FindStringSubmatch(s); c != nil {
			h, _ := strconv.Atoi(c[1])
			m, _ := strconv.Atoi(c[2])
			if h > 23 || m > 59 {
				return 0, false
			}
			ms, err := reminders.LocalToUTC(LocalDateOf(now, tz)+"T"+pad2(h)+":"+c[2], tz)
			return ms, err == nil
		}
		if absDTRe.MatchString(s) {
			return dateParse(s)
		}
		if localDTRe.MatchString(s) {
			r := []rune(s)
			if r[10] == ' ' {
				r[10] = 'T'
			}
			ms, err := reminders.LocalToUTC(string(r[:16]), tz)
			return ms, err == nil
		}
	}
	return 0, false
}

func LocalDateOf(ms int64, tz string) string { return reminders.UTCToLocal(ms, tz)[:10] }
func LocalTimeOf(ms int64, tz string) string { return reminders.UTCToLocal(ms, tz)[11:16] }

// WeekdayOfMs is weekdayOfMs: MO..SU in tz.
func WeekdayOfMs(ms int64, tz string) string {
	wd := time.UnixMilli(ms).In(zone.MustLoad(tz)).Weekday()
	return DayCodes[(int(wd)+6)%7]
}

// CalendarDaysBetween is calendarDaysBetween: whole calendar days from the
// date of `from` to the date of `to`, both in tz.
func CalendarDaysBetween(from, to int64, tz string) int64 {
	l := zone.MustLoad(tz)
	a := time.UnixMilli(from).In(l)
	b := time.UnixMilli(to).In(l)
	da := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	db := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int64(db.Sub(da).Hours() / 24)
}

// NowScope is nowScope: now.* as templates see it.
func NowScope(now int64, tz string) *jsonx.Object {
	return obj("at", float64(now), "date", LocalDateOf(now, tz), "time", LocalTimeOf(now, tz), "weekday", WeekdayOfMs(now, tz))
}

var numericRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// Numeric is numeric: a finite number, or a string that is one.
func Numeric(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return 0, false
		}
		return t, true
	case string:
		s := strx.Trim(t)
		if s != "" && numericRe.MatchString(s) {
			f, err := strconv.ParseFloat(s, 64)
			// "-0" is -0 in JS, which JSON writes as 0; Go would write -0.
			return noNegZero(f), err == nil
		}
	}
	return 0, false
}

// CompactJSON is compactJson: JSON.stringify, "null" for undefined.
func CompactJSON(v any) string {
	if isUndef(v) {
		return "null"
	}
	b, err := jsonx.Marshal(v)
	if err != nil {
		return `"[unserializable]"`
	}
	return string(b)
}
