package flows

// Time sources (sources.ts): when a flow schedule fires. Each kind answers
// "which occurrences come after this cursor" from its config plus the cached
// state. Only the tool kind refreshes (it reads a connector tool).
//
// An occurrence key is "<local date>:<item>" (or the local datetime for
// RRULE); the engine stores one run per key, so the same item on the same day
// never fires twice.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// Occurrence is one fire time of a schedule trigger.
type Occurrence = db.FlowOcc

// Cursor is SourceCursor.
type Cursor = db.FlowCursor

// SourceInput is SourceInput. State is the trigger row's source_state.
type SourceInput struct {
	Source  Source
	TZ      string
	Days    []string
	HasDays bool
	State   *jsonx.Object
}

// SourceError carries a message about a tool source's result.
type SourceError struct{ Msg string }

func (e *SourceError) Error() string { return e.Msg }

const horizonDays = 8

// SourceEntry is one cached occurrence of a tool source.
type SourceEntry struct {
	Date  string
	Key   string
	Label string
	At    int64
}

func (e SourceEntry) value() *jsonx.Object {
	return obj("date", e.Date, "key", e.Key, "label", e.Label, "at", float64(e.At))
}

func stateEntries(state *jsonx.Object) []SourceEntry {
	var out []SourceEntry
	for _, x := range garr(state, "entries") {
		o, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		at, _ := objNum(o, "at")
		out = append(out, SourceEntry{Date: gstr(o, "date"), Key: gstr(o, "key"), Label: gstr(o, "label"), At: int64(at)})
	}
	return out
}

func entriesValue(entries []SourceEntry) []any {
	out := make([]any, len(entries))
	for i, e := range entries {
		out[i] = e.value()
	}
	return out
}

func stateFetched(state *jsonx.Object) []string {
	return gstrs(state, "fetched")
}

func stringsValue(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

func jsSortStrings(list []string) {
	sort.SliceStable(list, func(i, j int) bool { return strx.Compare16(list[i], list[j]) < 0 })
}

// --- local dates -------------------------------------------------------------------------

func LocalDate(ms int64, tz string) string { return LocalDateOf(ms, tz) }

// AddDays is addDays on an ISO date.
func AddDays(date string, n int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// NextLocalTime is nextLocalTime: the next instant after `after` whose local
// clock reads hh:mm.
func NextLocalTime(hhmm, tz string, after int64) int64 {
	d := LocalDate(after, tz)
	for i := 0; i < 3; i++ {
		at, err := reminders.LocalToUTC(d+"T"+hhmm, tz)
		if err == nil && at > after {
			return at
		}
		d = AddDays(d, 1)
	}
	return after + 24*3600_000
}

func isAfter(o Occurrence, c Cursor) bool {
	return o.At > c.At || (o.At == c.At && strx.Compare16(o.Key, c.Key) > 0)
}

func sortByTime(list []Occurrence) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].At != list[j].At {
			return list[i].At < list[j].At
		}
		return strx.Compare16(list[i].Key, list[j].Key) < 0
	})
}

func dayBased(cursor Cursor, tz string, perDate func(date string) ([]Occurrence, error)) ([]Occurrence, error) {
	start := AddDays(LocalDate(cursor.At, tz), -1)
	var out []Occurrence
	for i := 0; i <= horizonDays; i++ {
		list, err := perDate(AddDays(start, i))
		if err != nil {
			return nil, err
		}
		for _, o := range list {
			if isAfter(o, cursor) {
				out = append(out, o)
			}
		}
	}
	sortByTime(out)
	return out, nil
}

func clockLabel(hhmm string) string { return strings.Replace(hhmm, ":", ".", 1) }

func mustLocal(local, tz string) (int64, error) { return reminders.LocalToUTC(local, tz) }

func timesAfter(i SourceInput, cursor Cursor) ([]Occurrence, error) {
	return dayBased(cursor, i.TZ, func(date string) ([]Occurrence, error) {
		var out []Occurrence
		for _, t := range i.Source.Times {
			at, err := mustLocal(date+"T"+t.Time, i.TZ)
			if err != nil {
				return nil, err
			}
			l := clockLabel(t.Time)
			if t.Label != nil {
				l = *t.Label
			}
			out = append(out, Occurrence{At: at, Key: date + ":" + t.Time, Label: l})
		}
		return out, nil
	})
}

func rruleAfter(i SourceInput, cursor Cursor, limit int) ([]Occurrence, error) {
	rr := i.Source.RRule
	ats, err := reminders.OccurrencesAfter(reminders.Schedule{RRule: &rr, TZ: i.TZ, LocalStart: i.Source.Start}, cursor.At-1, limit+1)
	if err != nil {
		return nil, err
	}
	var out []Occurrence
	for _, ms := range ats {
		local := reminders.UTCToLocal(ms, i.TZ)
		l := clockLabel(local[11:16])
		if i.Source.Label != nil {
			l = *i.Source.Label
		}
		o := Occurrence{At: ms, Key: local[:16], Label: l}
		if isAfter(o, cursor) {
			out = append(out, o)
		}
	}
	return out, nil
}

// toolEntriesFor: cached entries for one date; a date with no data reuses the
// clock times of the closest fetched date, marked as fallback.
func toolEntriesFor(i SourceInput, date string) ([]Occurrence, error) {
	entries := stateEntries(i.State)
	fetched := append([]string{}, stateFetched(i.State)...)
	jsSortStrings(fetched)
	var out []Occurrence
	if containsStr(fetched, date) {
		for _, e := range entries {
			if e.Date == date {
				out = append(out, Occurrence{At: e.At, Key: e.Date + ":" + e.Key, Label: e.Label})
			}
		}
		return out, nil
	}
	if len(fetched) == 0 {
		return nil, nil
	}
	base := fetched[0]
	for k := len(fetched) - 1; k >= 0; k-- {
		if strx.Compare16(fetched[k], date) < 0 {
			base = fetched[k]
			break
		}
	}
	for _, e := range entries {
		if e.Date != base {
			continue
		}
		at, err := mustLocal(date+"T"+reminders.UTCToLocal(e.At, i.TZ)[11:16], i.TZ)
		if err != nil {
			return nil, err
		}
		out = append(out, Occurrence{At: at, Key: date + ":" + e.Key, Label: e.Label, Fallback: true})
	}
	return out, nil
}

func toolAfter(i SourceInput, cursor Cursor) ([]Occurrence, error) {
	if i.Source.Items != nil {
		return dayBased(cursor, i.TZ, func(date string) ([]Occurrence, error) { return toolEntriesFor(i, date) })
	}
	var out []Occurrence
	for _, e := range stateEntries(i.State) {
		o := Occurrence{At: e.At, Key: e.Date + ":" + e.Key, Label: e.Label}
		if isAfter(o, cursor) {
			out = append(out, o)
		}
	}
	sortByTime(out)
	return out, nil
}

func rawAfter(i SourceInput, cursor Cursor, limit int) ([]Occurrence, error) {
	switch i.Source.Kind {
	case "times":
		return timesAfter(i, cursor)
	case "rrule":
		return rruleAfter(i, cursor, limit)
	case "tool":
		return toolAfter(i, cursor)
	}
	return nil, nil
}

// Upcoming is upcoming: the next n occurrences strictly after the cursor that
// fall on an active day.
func Upcoming(i SourceInput, cursor Cursor, n int) ([]Occurrence, error) {
	out := []Occurrence{}
	c := cursor
	for round := 0; round < 60 && len(out) < n; round++ {
		batch, err := rawAfter(i, c, n*2)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, o := range batch {
			if len(out) >= n {
				break
			}
			if !i.HasDays || containsStr(i.Days, WeekdayOfMs(o.At, i.TZ)) {
				out = append(out, o)
			}
		}
		last := batch[len(batch)-1]
		c = Cursor{At: last.At, Key: last.Key}
	}
	return out, nil
}

// NextOccurrenceAfter is nextOccurrenceAfter.
func NextOccurrenceAfter(i SourceInput, cursor Cursor) (*Occurrence, error) {
	list, err := Upcoming(i, cursor, 1)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// --- tool results ----------------------------------------------------------------------

func contentTexts(o *jsonx.Object) (string, bool) {
	arr, ok := objGet(o, "content").([]any)
	if !ok {
		return "", false
	}
	var parts []string
	for _, c := range arr {
		if objStr(c, "type") == "text" {
			if s, ok := objGet(c, "text").(string); ok {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n"), true
}

// ToolData is toolData: MCP structuredContent, the parsed text content, or
// the value itself.
func ToolData(output any) (any, error) {
	v, err := normalizeJSON(output)
	if err != nil {
		return nil, err
	}
	if o, ok := v.(*jsonx.Object); ok {
		texts, hasTexts := contentTexts(o)
		// The error text is the server's, so it isn't repeated.
		if jsonx.Truthy(objGet(o, "isError")) {
			return nil, &SourceError{"The tool returned an error result. Check the args with test_flow."}
		}
		if sc := objGet(o, "structuredContent"); isObjectish(sc) {
			return sc, nil
		}
		if hasTexts {
			p, err := jsonx.Parse([]byte(texts))
			if err != nil {
				return nil, &SourceError{"The tool result is not JSON, so no times can be read from it."}
			}
			return p, nil
		}
	}
	if s, ok := v.(string); ok {
		p, err := jsonx.Parse([]byte(s))
		if err != nil {
			return nil, &SourceError{"The tool result is not JSON, so no times can be read from it."}
		}
		return p, nil
	}
	return v, nil
}

// GetPath is getPath: "times.start", "data.0.due", "" (the value itself).
// ok false is undefined.
func GetPath(data any, path string) (any, bool) {
	cur := data
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		switch c := cur.(type) {
		case *jsonx.Object:
			v, ok := c.Get(part)
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			if part == "length" {
				cur = float64(len(c))
				continue
			}
			if !digitsRe.MatchString(part) {
				return nil, false
			}
			n, _ := strconv.Atoi(part)
			if n >= len(c) {
				return nil, false
			}
			cur = c[n]
		default:
			return nil, false
		}
	}
	return cur, true
}

// ParseTimeValue is parseTimeValue: a value from a tool result -> UTC ms.
// ok false is null; an error is a date that doesn't exist (TS threw).
func ParseTimeValue(v any, date, tz, dateTime string) (int64, bool, error) {
	if dateTime == "" {
		dateTime = "09:00"
	}
	switch t := v.(type) {
	case float64:
		ms, ok := ToMs(t, tz, 0)
		return ms, ok, nil
	case string:
		s := strx.Trim(t)
		if c := clockRe.FindStringSubmatch(s); c != nil {
			h, _ := strconv.Atoi(c[1])
			m, _ := strconv.Atoi(c[2])
			if h > 23 || m > 59 {
				return 0, false, nil
			}
			ms, err := reminders.LocalToUTC(date+"T"+pad2(h)+":"+c[2], tz)
			return ms, err == nil, err
		}
		if dateRe.MatchString(s) {
			ms, err := reminders.LocalToUTC(s+"T"+dateTime, tz)
			return ms, err == nil, err
		}
		if absDTRe.MatchString(s) {
			ms, ok := dateParse(s)
			return ms, ok, nil
		}
		if localDTRe.MatchString(s) {
			r := []rune(s)
			if r[10] == ' ' {
				r[10] = 'T'
			}
			ms, err := reminders.LocalToUTC(string(r[:16]), tz)
			return ms, err == nil, err
		}
	}
	return 0, false, nil
}

var labelJunk = regexp.MustCompile("[\\x00-\\x1f\\x7f`*_~<>\\[\\]{}]")

// SafeLabel is safeLabel: one short plain line, at most 60 characters.
func SafeLabel(v any) *string {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case float64:
		s = jsonx.NumberString(t)
	default:
		return nil
	}
	s = strx.Slice16(strx.Trim(oneLine(labelJunk.ReplaceAllString(s, " "))), 60)
	if s == "" {
		return nil
	}
	return &s
}

// Extracted is Extracted.
type Extracted struct {
	Entries  []SourceEntry
	Problems []string
}

// ExtractItems is extractItems.
func ExtractItems(src Source, data any, date, tz string) (Extracted, error) {
	var out Extracted
	for _, item := range src.Items {
		raw, present := GetPath(data, item.Field)
		var at int64
		ok := false
		if present {
			var err error
			at, ok, err = ParseTimeValue(raw, date, tz, "")
			if err != nil {
				return out, err
			}
		}
		if !ok {
			why := "not a time"
			if !present {
				why = "missing"
			}
			out.Problems = append(out.Problems, item.Field+": "+why)
			continue
		}
		parts := strings.Split(item.Field, ".")
		label := parts[len(parts)-1]
		if item.Label != nil {
			label = *item.Label
		}
		out.Entries = append(out.Entries, SourceEntry{Date: date, Key: item.Field, Label: label, At: at + int64(item.OffsetMin)*60_000})
	}
	return out, nil
}

const MaxListEntries = 200

// ExtractList is extractList.
func ExtractList(src Source, data any, tz string) (Extracted, error) {
	var out Extracted
	list := src.List
	if list == nil {
		return out, nil
	}
	arrV, _ := GetPath(data, list.Path)
	arr, ok := arrV.([]any)
	if !ok {
		p := list.Path
		if p == "" {
			p = "(result)"
		}
		out.Problems = append(out.Problems, p+": not an array")
		return out, nil
	}
	seen := map[string]bool{}
	if len(arr) > MaxListEntries {
		arr = arr[:MaxListEntries]
	}
	for idx, el := range arr {
		raw, present := GetPath(el, list.At)
		var base int64
		ok := false
		if present {
			var err error
			base, ok, err = ParseTimeValue(raw, "1970-01-01", tz, list.Time)
			if err != nil {
				return out, err
			}
		}
		if s, isStr := raw.(string); !ok || (present && isStr && clockRe.MatchString(strx.Trim(s))) {
			out.Problems = append(out.Problems, fmt.Sprintf("[%d].%s: not a date", idx, list.At))
			continue
		}
		at := base + int64(list.OffsetMin)*60_000
		var label *string
		if list.Label != nil {
			lv, _ := GetPath(el, *list.Label)
			label = SafeLabel(lv)
		}
		l := fmt.Sprintf("#%d", idx+1)
		if label != nil {
			l = *label
		}
		key := reminders.UTCToLocal(at, tz)[11:16] + ":" + l
		day := LocalDate(at, tz)
		for seen[day+":"+key] {
			key += "+"
		}
		seen[day+":"+key] = true
		out.Entries = append(out.Entries, SourceEntry{Date: day, Key: key, Label: l, At: at})
	}
	return out, nil
}

// RenderArgs is renderArgs: date placeholders filled in, recursively.
func RenderArgs(args *jsonx.Object, date, tz string) *jsonx.Object {
	var fill func(v any) any
	fill = func(v any) any {
		switch t := v.(type) {
		case string:
			r := strings.NewReplacer("{{date}}", date, "{{month}}", date[:7], "{{year}}", date[:4], "{{tz}}", tz)
			return r.Replace(t)
		case []any:
			out := make([]any, len(t))
			for i, x := range t {
				out[i] = fill(x)
			}
			return out
		case *jsonx.Object:
			out := jsonx.NewMap[any]()
			for _, k := range t.Keys() {
				x, _ := t.Get(k)
				out.Set(k, fill(x))
			}
			return out
		}
		return v
	}
	if args == nil {
		return jsonx.NewMap[any]()
	}
	return fill(args).(*jsonx.Object)
}

// ToolCaller is the flows/sources ToolCaller (tools.ToolCaller).
type ToolCaller = tools.ToolCaller

// RefreshResult is RefreshResult.
type RefreshResult struct {
	State *jsonx.Object
	Error *string
}

// RefreshToolSource is refreshToolSource: today and tomorrow for per-day
// items, one call for a list. Whatever succeeded is merged in.
func RefreshToolSource(ctx context.Context, src Source, tz string, prev *jsonx.Object, call func(ctx context.Context, connectorID, tool string, args *jsonx.Object) (any, error), now int64) RefreshResult {
	if prev == nil {
		prev = jsonx.NewMap[any]()
	}
	today := LocalDate(now, tz)
	keepFrom := AddDays(today, -3)
	entries := stateEntries(prev)
	fetched := append([]string{}, stateFetched(prev)...)
	var errs []string
	attempt := func(date string, extract func(data any) (Extracted, error)) []SourceEntry {
		fail := func(msg string) []SourceEntry {
			errs = append(errs, strx.Slice16(date+": "+msg, 300))
			return nil
		}
		out, err := call(ctx, src.ConnectorID, src.Tool, RenderArgs(src.Args, date, tz))
		if err != nil {
			return fail(err.Error())
		}
		data, err := ToolData(out)
		if err != nil {
			return fail(err.Error())
		}
		got, err := extract(data)
		if err != nil {
			return fail(err.Error())
		}
		if len(got.Entries) == 0 {
			probs := got.Problems
			if len(probs) > 3 {
				probs = probs[:3]
			}
			detail := strings.Join(probs, "; ")
			if detail == "" {
				detail = "empty"
			}
			return fail(fmt.Sprintf("No usable times in the result (%s).", detail))
		}
		return got.Entries
	}
	if src.Items != nil {
		for _, date := range []string{today, AddDays(today, 1)} {
			got := attempt(date, func(d any) (Extracted, error) { return ExtractItems(src, d, date, tz) })
			if got == nil {
				continue
			}
			var kept []SourceEntry
			for _, e := range entries {
				if e.Date != date {
					kept = append(kept, e)
				}
			}
			entries = append(kept, got...)
			if !containsStr(fetched, date) {
				fetched = append(fetched, date)
			}
		}
		sorted := append([]string{}, fetched...)
		jsSortStrings(sorted)
		latest := ""
		if len(sorted) > 0 {
			latest = sorted[len(sorted)-1]
		}
		var keep []string
		for _, d := range fetched {
			if strx.Compare16(d, keepFrom) >= 0 || d == latest {
				keep = append(keep, d)
			}
		}
		jsSortStrings(keep)
		fetched = keep
		var kept []SourceEntry
		for _, e := range entries {
			if containsStr(fetched, e.Date) {
				kept = append(kept, e)
			}
		}
		entries = kept
	} else {
		got := attempt(today, func(d any) (Extracted, error) { return ExtractList(src, d, tz) })
		if got != nil {
			var kept []SourceEntry
			for _, e := range got {
				if e.At >= now-3*24*3600_000 {
					kept = append(kept, e)
				}
			}
			entries = kept
			fetched = []string{today}
		}
	}
	var errText *string
	if len(errs) > 0 {
		s := strings.Join(errs, " | ")
		errText = &s
	}
	state := spread(prev)
	state.Set("entries", entriesValue(entries))
	state.Set("fetched", stringsValue(fetched))
	if errText != nil {
		f, _ := objNum(prev, "failures")
		state.Set("failures", f+1)
		state.Set("lastError", *errText)
		if v, ok := prev.Get("lastOkAt"); ok && v != nil {
			state.Set("lastOkAt", v)
		} else {
			state.Set("lastOkAt", nil)
		}
	} else {
		state.Set("failures", 0.0)
		state.Set("lastError", nil)
		state.Set("lastOkAt", float64(now))
	}
	return RefreshResult{State: state, Error: errText}
}
