package ics

// A small iCalendar reader (RFC 5545): just what listing a feed's events in a
// window needs. It unfolds lines, reads VEVENT properties with their
// parameters, expands RRULE with EXDATE and RECURRENCE-ID overrides, and
// resolves TZID through the IANA database (what Google, iCloud, Fastmail and
// Nextcloud feeds use), with a few Windows zone names Outlook writes.

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// Event is one occurrence in a window.
type Event struct {
	ID          string
	Title       string
	Start       time.Time
	End         time.Time
	AllDay      bool
	Location    string
	Description string
	URL         string
	Status      string
	Declined    bool
	Recurring   bool
}

type prop struct {
	name   string
	params map[string]string
	value  string
}

type vevent struct {
	props []prop
}

func (v vevent) get(name string) (prop, bool) {
	for _, p := range v.props {
		if p.name == name {
			return p, true
		}
	}
	return prop{}, false
}

func (v vevent) all(name string) []prop {
	var out []prop
	for _, p := range v.props {
		if p.name == name {
			out = append(out, p)
		}
	}
	return out
}

// ErrNotCalendar is the refusal for a body that isn't iCalendar.
var ErrNotCalendar = errors.New("The link didn't return a calendar (no BEGIN:VCALENDAR). Use the secret iCal address, not the calendar's web page.")

// unfold joins continuation lines (a line starting with a space or a tab).
func unfold(body string) []string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		out = append(out, l)
	}
	return out
}

// parseLine reads NAME;PARAM=V;PARAM="V":VALUE.
func parseLine(l string) (prop, bool) {
	inQuote := false
	colon := -1
	for i, r := range l {
		if r == '"' {
			inQuote = !inQuote
		} else if r == ':' && !inQuote {
			colon = i
			break
		}
	}
	if colon <= 0 {
		return prop{}, false
	}
	head, value := l[:colon], l[colon+1:]
	parts := splitParams(head)
	p := prop{name: strings.ToUpper(parts[0]), params: map[string]string{}, value: value}
	for _, kv := range parts[1:] {
		if k, v, ok := strings.Cut(kv, "="); ok {
			p.params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return p, true
}

func splitParams(head string) []string {
	var out []string
	inQuote := false
	start := 0
	for i, r := range head {
		if r == '"' {
			inQuote = !inQuote
		} else if r == ';' && !inQuote {
			out = append(out, head[start:i])
			start = i + 1
		}
	}
	return append(out, head[start:])
}

// text unescapes a TEXT value.
func text(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
}

// parse reads the VEVENTs of a calendar body.
func parse(body string) ([]vevent, error) {
	lines := unfold(body)
	found := false
	var out []vevent
	var cur *vevent
	depth := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		p, ok := parseLine(l)
		if !ok {
			continue
		}
		switch {
		case p.name == "BEGIN" && strings.EqualFold(p.value, "VCALENDAR"):
			found = true
		case p.name == "BEGIN" && strings.EqualFold(p.value, "VEVENT") && cur == nil:
			cur = &vevent{}
			depth = 0
		case p.name == "BEGIN" && cur != nil:
			depth++ // VALARM inside an event
		case p.name == "END" && cur != nil && depth > 0:
			depth--
		case p.name == "END" && strings.EqualFold(p.value, "VEVENT") && cur != nil:
			out = append(out, *cur)
			cur = nil
		case cur != nil && depth == 0:
			cur.props = append(cur.props, p)
		}
	}
	if !found {
		return nil, ErrNotCalendar
	}
	return out, nil
}

// windowsZones maps the Windows zone names Outlook feeds use to IANA names.
var windowsZones = map[string]string{
	"SE Asia Standard Time":          "Asia/Bangkok",
	"Singapore Standard Time":        "Asia/Singapore",
	"China Standard Time":            "Asia/Shanghai",
	"Tokyo Standard Time":            "Asia/Tokyo",
	"Korea Standard Time":            "Asia/Seoul",
	"India Standard Time":            "Asia/Kolkata",
	"W. Australia Standard Time":     "Australia/Perth",
	"AUS Eastern Standard Time":      "Australia/Sydney",
	"GMT Standard Time":              "Europe/London",
	"W. Europe Standard Time":        "Europe/Berlin",
	"Romance Standard Time":          "Europe/Paris",
	"Central Europe Standard Time":   "Europe/Budapest",
	"Eastern Standard Time":          "America/New_York",
	"Central Standard Time":          "America/Chicago",
	"Mountain Standard Time":         "America/Denver",
	"Pacific Standard Time":          "America/Los_Angeles",
	"UTC":                            "UTC",
	"Arab Standard Time":             "Asia/Riyadh",
	"Arabian Standard Time":          "Asia/Dubai",
	"Taipei Standard Time":           "Asia/Taipei",
	"North Asia East Standard Time":  "Asia/Irkutsk",
	"New Zealand Standard Time":      "Pacific/Auckland",
	"E. South America Standard Time": "America/Sao_Paulo",
}

func location(tzid string, fallback *time.Location) *time.Location {
	if tzid == "" {
		return fallback
	}
	tzid = strings.TrimPrefix(tzid, "/")
	if l, err := time.LoadLocation(tzid); err == nil {
		return l
	}
	if name, ok := windowsZones[tzid]; ok {
		if l, err := time.LoadLocation(name); err == nil {
			return l
		}
	}
	return fallback
}

// when reads a DATE or DATE-TIME property. allDay is true for a DATE.
func when(p prop, fallback *time.Location) (time.Time, bool, bool) {
	v := strings.TrimSpace(p.value)
	if p.params["VALUE"] == "DATE" || len(v) == 8 {
		t, err := time.ParseInLocation("20060102", v, fallback)
		return t, true, err == nil
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t, false, err == nil
	}
	t, err := time.ParseInLocation("20060102T150405", v, location(p.params["TZID"], fallback))
	return t, false, err == nil
}

var durRe = regexp.MustCompile(`^([+-])?P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func duration(v string) (time.Duration, bool) {
	m := durRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, false
	}
	n := func(s string) time.Duration {
		var x int
		for _, c := range s {
			x = x*10 + int(c-'0')
		}
		return time.Duration(x)
	}
	d := n(m[2])*7*24*time.Hour + n(m[3])*24*time.Hour + n(m[4])*time.Hour + n(m[5])*time.Minute + n(m[6])*time.Second
	if m[1] == "-" {
		d = -d
	}
	return d, true
}

// joinLinkRe finds a video call link in a description or location.
var joinLinkRe = regexp.MustCompile(`https://(?:meet\.google\.com|(?:[a-z0-9-]+\.)?zoom\.us|teams\.microsoft\.com|teams\.live\.com|meet\.jit\.si|[a-z0-9-]+\.webex\.com)/[^\s<>"')\]]+`)

// declined: an ATTENDEE whose address is one of the owner's, with
// PARTSTAT=DECLINED.
func declined(v vevent, emails []string) bool {
	if len(emails) == 0 {
		return false
	}
	for _, a := range v.all("ATTENDEE") {
		addr := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(a.value, "mailto:"), "MAILTO:"))
		for _, e := range emails {
			if addr == e && strings.EqualFold(a.params["PARTSTAT"], "DECLINED") {
				return true
			}
		}
	}
	return false
}

// Events lists the occurrences that overlap [from, to), sorted by start.
// Cancelled events are left out; declined ones are marked.
func Events(body string, from, to time.Time, tz *time.Location, emails []string) ([]Event, error) {
	list, err := parse(body)
	if err != nil {
		return nil, err
	}
	for i := range emails {
		emails[i] = strings.ToLower(strings.TrimSpace(emails[i]))
	}
	// Overrides: UID + RECURRENCE-ID replace one occurrence of a series.
	overridden := map[string]bool{}
	for _, v := range list {
		if rid, ok := v.get("RECURRENCE-ID"); ok {
			uid, _ := v.get("UID")
			if t, _, ok := when(rid, tz); ok {
				overridden[uid.value+"|"+t.UTC().Format(time.RFC3339)] = true
			}
		}
	}
	var out []Event
	for _, v := range list {
		uid, _ := v.get("UID")
		ds, ok := v.get("DTSTART")
		if !ok {
			continue
		}
		start, allDay, ok := when(ds, tz)
		if !ok {
			continue
		}
		end := start
		if de, ok := v.get("DTEND"); ok {
			if t, _, ok := when(de, tz); ok {
				end = t
			}
		} else if du, ok := v.get("DURATION"); ok {
			if d, ok := duration(du.value); ok {
				end = start.Add(d)
			}
		} else if allDay {
			end = start.AddDate(0, 0, 1)
		}
		length := end.Sub(start)
		status := ""
		if s, ok := v.get("STATUS"); ok {
			status = strings.ToLower(strings.TrimSpace(s.value))
		}
		if status == "cancelled" {
			continue
		}
		base := Event{ID: uid.value, AllDay: allDay, Status: status, Declined: declined(v, emails)}
		if p, ok := v.get("SUMMARY"); ok {
			base.Title = text(p.value)
		}
		if p, ok := v.get("LOCATION"); ok {
			base.Location = text(p.value)
		}
		if p, ok := v.get("DESCRIPTION"); ok {
			base.Description = text(p.value)
		}
		for _, name := range []string{"X-GOOGLE-CONFERENCE", "X-MICROSOFT-SKYPETEAMSMEETINGURL", "URL"} {
			if p, ok := v.get(name); ok && base.URL == "" {
				base.URL = strings.TrimSpace(p.value)
			}
		}
		if base.URL == "" {
			base.URL = joinLinkRe.FindString(base.Description + " " + base.Location)
		}
		_, isOverride := v.get("RECURRENCE-ID")
		rr, hasRule := v.get("RRULE")
		if !hasRule || isOverride {
			if start.Before(to) && (end.After(from) || (end.Equal(start) && !start.Before(from))) {
				e := base
				e.Start, e.End = start, end
				if isOverride {
					e.Recurring = true
					e.ID = base.ID + "_" + start.UTC().Format("20060102T150405Z")
				}
				out = append(out, e)
			}
			continue
		}
		occ, err := expand(rr.value, start, v.all("EXDATE"), from.Add(-length), to, tz)
		if err != nil {
			continue
		}
		for _, t := range occ {
			if overridden[uid.value+"|"+t.UTC().Format(time.RFC3339)] {
				continue
			}
			e := base
			e.Start, e.End, e.Recurring = t, t.Add(length), true
			e.ID = base.ID + "_" + t.UTC().Format("20060102T150405Z")
			if e.Start.Before(to) && e.End.After(from) {
				out = append(out, e)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// MaxOccurrences caps one series in one window.
const MaxOccurrences = 500

func expand(rule string, start time.Time, exdates []prop, from, to time.Time, tz *time.Location) ([]time.Time, error) {
	opt, err := rrule.StrToROptionInLocation(rule, start.Location())
	if err != nil {
		return nil, err
	}
	opt.Dtstart = start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, err
	}
	set := rrule.Set{}
	set.RRule(r)
	for _, ex := range exdates {
		for _, v := range strings.Split(ex.value, ",") {
			if t, _, ok := when(prop{name: "EXDATE", params: ex.params, value: v}, tz); ok {
				set.ExDate(t)
			}
		}
	}
	list := set.Between(from, to, true)
	if len(list) > MaxOccurrences {
		list = list[:MaxOccurrences]
	}
	return list, nil
}
