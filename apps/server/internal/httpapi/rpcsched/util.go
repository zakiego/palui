package rpcsched

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jobs"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/outbound"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/watches"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

// obj builds an ordered JSON object from key, value pairs (the TS literal's
// key order).
func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// friendly turns a domain error meant for the owner into the message the
// dashboard shows (a thrown Error's message under TanStack Start); anything
// else stays a 500.
func friendly(err error) error {
	if err == nil {
		return nil
	}
	var oe *outbound.OutboundError
	var je *jobs.JobError
	var re *reminders.ReminderError
	var te *reminders.TimeError
	var we *watches.WatchError
	switch {
	case errors.As(err, &oe):
		return httpapi.Fail("%s", oe.Msg)
	case errors.As(err, &je):
		return httpapi.Fail("%s", je.Msg)
	case errors.As(err, &re):
		return httpapi.Fail("%s", re.Msg)
	case errors.As(err, &te):
		return httpapi.Fail("%s", te.Msg)
	case errors.As(err, &we):
		return httpapi.Fail("%s", we.Msg)
	}
	return err
}

// poke is the bridges' runtime().reconcile().catch(() => {}): after a reminder
// or job write, re-arm the scheduler at once. A missing runtime is no error.
func poke(c *httpapi.Call) {
	if rt, err := c.Runtime(); err == nil {
		rt.Reconcile()
	}
}

var utcSpecifier = regexp.MustCompile(`(?i)^utc(?:([+-]\d{1,2})(?::(\d{2}))?)?$`)

// luxonZone is DateTime.local().setZone(tz): the zone luxon reads tz as, nil
// when it's invalid. "local", "system" and "default" are the process zone
// (UTC in the container), "utc"/"gmt" and "utc+N[:MM]" are fixed offsets, and
// anything else is an Intl zone name.
func luxonZone(tz string) *time.Location {
	l := strings.ToLower(tz)
	switch l {
	case "default", "local", "system", "utc", "gmt":
		return time.UTC
	}
	if m := utcSpecifier.FindStringSubmatch(l); m != nil {
		h, _ := strconv.Atoi(m[1])
		mins := 0
		if m[2] != "" {
			mins, _ = strconv.Atoi(m[2])
		}
		off := h*60 + mins
		if strings.HasPrefix(m[1], "-") {
			off = h*60 - mins
		}
		return time.FixedZone(tz, off*60)
	}
	if loc, ok := zone.Load(tz); ok {
		return loc
	}
	return nil
}

func luxonZoneValid(tz string) bool { return luxonZone(tz) != nil }

// luxon's ISO 8601 grammar for dates with an optional time and offset: dashes
// and colons optional, a 6-digit signed year, fractions after "." or ",".
var isoRe = regexp.MustCompile(`(?i)^([+-]\d{6}|\d{4})(?:-?(\d\d)(?:-?(\d\d))?)?(?:T(\d\d)(?::?(\d\d)(?::?(\d\d)(?:[.,](\d{1,30}))?)?)?(?:(Z)|([+-]\d\d)(?::?(\d\d))?)?)?$`)

// fromISOSetZone is DateTime.fromISO(s, { setZone: true }).toMillis(): an
// offset gives the instant, a wall time is read in the process zone (UTC).
// ok is false when luxon would call the result invalid.
func fromISOSetZone(s string) (int64, bool) {
	m := isoRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n := func(x string, def int) int {
		if x == "" {
			return def
		}
		v, _ := strconv.Atoi(x)
		return v
	}
	p := zone.Parts{Year: n(m[1], 0), Month: n(m[2], 1), Day: n(m[3], 1), Hour: n(m[4], 0), Minute: n(m[5], 0), Second: n(m[6], 0)}
	if m[7] != "" {
		p.Millisecond = n((m[7] + "00")[:3], 0)
	}
	days := time.Date(p.Year, time.Month(p.Month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if p.Month < 1 || p.Month > 12 || p.Day < 1 || p.Day > days || p.Minute > 59 || p.Second > 59 {
		return 0, false
	}
	// luxon takes 24:00:00.000 as midnight at the end of the day.
	if p.Hour > 24 || (p.Hour == 24 && (p.Minute != 0 || p.Second != 0 || p.Millisecond != 0)) {
		return 0, false
	}
	ts := zone.LocalTS(p)
	if m[9] != "" {
		h, _ := strconv.Atoi(m[9][1:])
		mins := n(m[10], 0)
		off := h*60 + mins
		if m[9][0] == '-' {
			off = -off
		}
		return ts - int64(off)*60_000, true
	}
	return ts, true
}

// wallTime is toFormat("yyyy-LL-dd'T'HH:mm:ss") in loc.
func wallTime(ms int64, loc *time.Location) string {
	return time.UnixMilli(ms).In(loc).Format("2006-01-02T15:04:05")
}

// jsString is String(v) for a JSON value.
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsonx.NumberString(t)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			if x != nil {
				parts[i] = jsString(x)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}
