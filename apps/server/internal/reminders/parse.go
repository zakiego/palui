package reminders

// Natural-language reminder parsing (reminders/parse.ts, PLAN 6) with the
// `fast` model. The model only reads the phrase into { text, localDateTime,
// rrule }; everything after that (the UTC instant, the first occurrence of a
// series, the echo) is computed here with time.go, so the model never does
// offset math.
//
// TS used generateText with Output.object (a non-streaming call). Go sends the
// same request through the streaming model interface with
// CallOptions.ResponseFormat and validates the object itself, with the AI
// SDK's error texts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// ParsedReminderSchema is parsedReminderSchema as the AI SDK sends it
// (z.toJSONSchema output, key order kept).
const ParsedReminderSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"text":{"type":"string","description":"What to remind about, in the user's language, without the time words or the 'remind me' part."},"localDateTime":{"type":"string","description":"Wall-clock date and time in the user's zone, ISO 8601 without offset: YYYY-MM-DDTHH:mm. For a repeating reminder, its first occurrence."},"rrule":{"description":"RRULE body without DTSTART for repeating reminders, e.g. \"FREQ=WEEKLY;BYDAY=MO\". null for a one-off.","type":["string","null"]}},"required":["text","localDateTime","rrule"],"additionalProperties":false}`

// ModelReminder is what the model returns.
type ModelReminder struct {
	Text          string  `json:"text"`
	LocalDateTime string  `json:"localDateTime"`
	RRule         *string `json:"rrule"`
}

// ParsedReminder is control.ts's ParsedReminder. Its JSON keeps the TS keys
// and order: {ok, text, fireAt, fireAtIso, rrule, tz, echo} on success,
// {ok, text, rrule?, tz, error} on failure.
type ParsedReminder struct {
	OK        bool
	Text      string
	FireAt    *int64
	FireAtIso string
	// RRule is written when HasRRule (null when RRule is nil).
	RRule    *string
	HasRRule bool
	TZ       string
	Echo     string
	Error    string
}

func (p ParsedReminder) MarshalJSON() ([]byte, error) {
	o := jsonx.NewMap[any]()
	o.Set("ok", p.OK)
	o.Set("text", p.Text)
	if p.FireAt != nil {
		o.Set("fireAt", *p.FireAt)
	}
	if p.FireAtIso != "" {
		o.Set("fireAtIso", p.FireAtIso)
	}
	if p.HasRRule {
		if p.RRule == nil {
			o.Set("rrule", nil)
		} else {
			o.Set("rrule", *p.RRule)
		}
	}
	o.Set("tz", p.TZ)
	if p.Echo != "" {
		o.Set("echo", p.Echo)
	}
	if p.Error != "" {
		o.Set("error", p.Error)
	}
	return jsonx.Marshal(o)
}

func (p *ParsedReminder) UnmarshalJSON(b []byte) error {
	var raw struct {
		OK        bool             `json:"ok"`
		Text      string           `json:"text"`
		FireAt    *int64           `json:"fireAt"`
		FireAtIso string           `json:"fireAtIso"`
		RRule     *json.RawMessage `json:"rrule"`
		TZ        string           `json:"tz"`
		Echo      string           `json:"echo"`
		Error     string           `json:"error"`
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*p = ParsedReminder{OK: raw.OK, Text: raw.Text, FireAt: raw.FireAt, FireAtIso: raw.FireAtIso, TZ: raw.TZ, Echo: raw.Echo, Error: raw.Error}
	if v, ok := keys["rrule"]; ok {
		p.HasRRule = true
		var s *string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		p.RRule = s
	}
	return nil
}

var enDays = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// luxon weekday: 1 = Monday.
func weekdayIdx(t time.Time) int { return (int(t.Weekday()) + 6) % 7 }

func plusDays(d time.Time, n int) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day()+n, 0, 0, 0, 0, time.UTC)
}

func ymd(t time.Time) string { return t.Format("2006-01-02") }

// calendar is a two-week calendar so the model never has to do weekday arithmetic.
func calendar(d time.Time) string {
	var lines, upcoming []string
	for i := 1; i <= 7; i++ {
		x := plusDays(d, i)
		upcoming = append(upcoming, fmt.Sprintf("%s/%s = %s", enDays[weekdayIdx(x)], WeekdaysID[weekdayIdx(x)], ymd(x)))
	}
	for i := 0; i < 15; i++ {
		x := plusDays(d, i)
		tag := ""
		switch i {
		case 0:
			tag = " (today)"
		case 1:
			tag = " (tomorrow, besok)"
		case 2:
			tag = " (day after tomorrow, lusa)"
		}
		lines = append(lines, fmt.Sprintf("- %s %s / %s%s", ymd(x), enDays[weekdayIdx(x)], WeekdaysID[weekdayIdx(x)], tag))
	}
	return strings.Join(lines, "\n") + "\n\nA bare weekday name means the next one after today: " + strings.Join(upcoming, ", ") + "."
}

// offsetZZ is luxon's "ZZ": +08:00.
func offsetZZ(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign = "-"
		off = -off
	}
	return fmt.Sprintf("%s%02d:%02d", sign, off/3600, (off%3600)/60)
}

func BuildSystemPrompt(tz string, now int64) string {
	l := loc(tz)
	d := time.UnixMilli(now).In(l)
	local := d.Format("2006-01-02T15:04")
	endOfMonth := ymd(time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC))
	wd := weekdayIdx(d) + 1
	n := (8 - wd) % 7
	if n == 0 {
		n = 7
	}
	nextMonday := ymd(plusDays(d, n))
	tomorrow := ymd(plusDays(d, 1))
	inTwoHours := time.UnixMilli(now + 2*3600_000).In(l).Format("2006-01-02T15:04")
	day := enDays[weekdayIdx(d)] + " / " + WeekdaysID[weekdayIdx(d)]
	return `You turn a reminder request (Indonesian or English, often casual) into JSON: { text, localDateTime, rrule }.

Current time: ` + local + ` (` + day + `), zone ` + tz + ` (` + ZoneLabel(tz, now) + `, UTC` + offsetZZ(d) + `).
This month ends on ` + endOfMonth + `. Next week starts on Monday ` + nextMonday + `.

Calendar:
` + calendar(d) + `

Output
- localDateTime: wall-clock time in ` + tz + `, "YYYY-MM-DDTHH:mm", no offset, no "Z". It must be after the current time.
- rrule: an RFC 5545 RRULE body without DTSTART, or null for a one-off. Use FREQ, INTERVAL, BYDAY, BYMONTHDAY, BYMONTH, BYHOUR, BYMINUTE, BYSETPOS, COUNT, UNTIL only.
- For a repeating reminder, localDateTime is the FIRST occurrence after the current time (it carries the time of day).
- text: the thing to remember, in the user's language, short. Drop "ingetin/ingatkan/remind me (to)", the time and the repeat words. Keep names and details.

Times
- "jam 9", "at 9", "9", "pukul 9" with no other hint: hours 7 to 11 mean morning (07:00 to 11:00), 12 means 12:00 noon, hours 1 to 6 mean afternoon (13:00 to 18:00).
- Explicit parts of day win: pagi/morning = AM; siang = around midday (jam 1 siang = 13:00); sore/afternoon = PM (jam 4 sore = 16:00); malam/evening/tonight/night = PM (jam 8 malam = 20:00, jam 9 malam = 21:00). "am"/"pm" win over everything. "jam 12 malam"/midnight = 00:00 of the next day.
- Indonesian "9.30" or "09.30" means 09:30. "setengah 10" means 09:30 (half before 10). "jam 10 lewat 15" means 10:15.
- A part of day with no hour: pagi/morning = 08:00, siang/noon = 12:00, sore/afternoon = 16:00, malam/evening/tonight = 20:00.
- A date with no time at all (besok, lusa, a weekday, a date, akhir bulan) means 09:00.
- If only a time is given (no day) and that time today has already passed, use tomorrow. If it's still ahead, use today. At ` + local + `, "jam 9" alone is tomorrow 09:00, "jam 3" alone is today 15:00.
- Relative times: "in 2 hours", "2 jam lagi", "30 menit lagi", "setengah jam lagi" (30 minutes), "1 jam 15 menit lagi", "in 90 minutes": add exactly that to the current time, keep the minutes exact.
- "in 3 days at 6pm"/"3 hari lagi jam 6 sore": that day at that time. "in 3 days" alone: that day at 09:00.

Days
- besok/tomorrow = +1 day, lusa/the day after tomorrow = +2 days. "nanti", "nanti sore", "nanti malam", "tonight", "this afternoon", "hari ini", "today" = today.
- A weekday name ("Jumat", "on Friday", "hari Kamis", "Jumat ini", "this Friday") means the next such day after today; if it's today's weekday, it means 7 days from today unless "hari ini"/"today" is said. "Senin depan", "next Monday", "minggu depan hari Senin" = the Monday of next week (` + nextMonday + `). "Thursday next week", "Kamis minggu depan" = that weekday in next week (the week starting ` + nextMonday + `).
- "tanggal 25"/"on the 25th" = the 25th of this month if still ahead, else next month. "25 Desember", "December 1st" = that date this year if still ahead, else next year. "akhir bulan"/"end of the month"/"the last day of the month" = the last day of this month. New Year's Eve = Dec 31; "tahun baru"/New Year's Day = Jan 1.

Repeats (rrule)
- "tiap/setiap/every day", "tiap hari", "every morning/evening at X" = FREQ=DAILY.
- "tiap Senin", "every Monday" = FREQ=WEEKLY;BYDAY=MO. Several days: "tiap Selasa dan Kamis" = FREQ=WEEKLY;BYDAY=TU,TH. Weekend = BYDAY=SA,SU.
- "hari kerja", "every weekday", "Senin sampai Jumat" = FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR.
- "tiap 2 minggu (sekali) hari Sabtu", "every 2 weeks on Thursday", "every other Thursday" = FREQ=WEEKLY;INTERVAL=2;BYDAY=SA (or TH); the first occurrence is the next such day.
- "tiap tanggal 1", "every month on the 1st" = FREQ=MONTHLY;BYMONTHDAY=1. "the 31st" stays BYMONTHDAY=31 (months without it are skipped; the first occurrence is the next month that has a 31st).
- "tiap akhir bulan", "every last day of the month" = FREQ=MONTHLY;BYMONTHDAY=-1.
- "Jumat terakhir tiap bulan", "last Friday of the month" = FREQ=MONTHLY;BYDAY=-1FR. "Senin pertama tiap bulan", "first Monday of the month" = FREQ=MONTHLY;BYDAY=1MO.
- "tiap tahun 17 Agustus", "every year on March 3" = FREQ=YEARLY;BYMONTH=8;BYMONTHDAY=17.
- Several times a day: "tiap hari jam 8 pagi dan jam 8 malam" = FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0, localDateTime = the next of those times.
- "tiap 3 jam", "every 3 hours" = FREQ=HOURLY;INTERVAL=3, first occurrence = now + 3 hours. "tiap 30 menit" = FREQ=MINUTELY;INTERVAL=30.
- "sampai 31 Desember"/"until Dec 31" adds UNTIL=YYYYMMDDT235959Z with that date; "5 kali"/"5 times" adds COUNT=5.
- A one-off (no repeat words) has rrule null. Only tiap/setiap/every/each/"sekali"/daily/weekly/monthly make a repeat. "Hari Jumat", "Jumat ini", "akhir bulan", "on the last day of the month", "on the 15th" alone are one-offs.
- The first occurrence of a repeat can be today when its time is still ahead: "every Monday and Wednesday at 7pm" said on a Monday at 10:00 starts that same Monday at 19:00.

Examples (current time ` + local + `):
- "ingetin bayar listrik besok jam 9" -> {"text":"bayar listrik","localDateTime":"` + tomorrow + `T09:00","rrule":null}
- "remind me every Monday 8am to send the report" -> {"text":"send the report","localDateTime":"` + nextMonday + `T08:00","rrule":"FREQ=WEEKLY;BYDAY=MO"}
- "2 jam lagi angkat jemuran" -> {"text":"angkat jemuran","localDateTime":"` + inTwoHours + `","rrule":null}`
}

var offsetSuffix = regexp.MustCompile(`(?i)(?:Z|[+-]\d{2}:?\d{2})$`)

func stripOffset(s string) string { return offsetSuffix.ReplaceAllString(strx.Trim(s), "") }

// FinishParse is the deterministic half: validate the model's fields and
// compute the instant and echo. A non-TimeError comes back as err (TS threw).
func FinishParse(m ModelReminder, fallbackText, tz string, now int64) (ParsedReminder, error) {
	text := strx.Trim(m.Text)
	if text == "" {
		text = strx.Trim(fallbackText)
	}
	var rr *string
	res, err := func() (ParsedReminder, error) {
		if err := AssertZone(tz); err != nil {
			return ParsedReminder{}, err
		}
		localStart, err := NormalizeLocal(stripOffset(m.LocalDateTime))
		if err != nil {
			return ParsedReminder{}, err
		}
		if m.RRule != nil && strx.Trim(*m.RRule) != "" {
			n, err := NormalizeRrule(*m.RRule)
			if err != nil {
				return ParsedReminder{}, err
			}
			rr = &n
		}
		// A one-off earlier today rolls to tomorrow (the echo says "Besok"); a
		// past or far-off time is refused. Same rules as Create.
		first, err := ResolveFirstFire(Schedule{RRule: rr, TZ: tz, LocalStart: localStart}, now)
		if err != nil {
			return ParsedReminder{}, err
		}
		s, fireAt := first.Schedule, first.FireAt
		if rr != nil {
			if today := TodayInstead(*rr, localStart, fireAt, now, tz); today != nil {
				s = *today
				occ, err := OccurrencesAfter(*today, now-1, 1)
				if err != nil {
					return ParsedReminder{}, err
				}
				fireAt = occ[0]
			}
		}
		echo, err := FormatEcho(s, fireAt, now)
		if err != nil {
			return ParsedReminder{}, err
		}
		if s.RRule != nil && *s.RRule != "" {
			start, err := FormatSeriesStart(s, fireAt, now)
			if err != nil {
				return ParsedReminder{}, err
			}
			echo += ", " + start
		}
		at := fireAt
		return ParsedReminder{OK: true, Text: text, FireAt: &at, FireAtIso: UTCToISO(fireAt, tz), RRule: rr, HasRRule: true, TZ: tz, Echo: echo}, nil
	}()
	if err == nil {
		return res, nil
	}
	var te *TimeError
	if errors.As(err, &te) {
		return ParsedReminder{OK: false, Text: text, RRule: rr, HasRRule: rr != nil, TZ: tz, Error: te.Msg}, nil
	}
	return ParsedReminder{}, err
}

type ParseOptions struct {
	// Default 15 s.
	Timeout time.Duration
	// nil means 1 (one retry of a retryable API error).
	MaxRetries *int
	// The first retry waits this long, doubling after (default 2 s).
	RetryDelay time.Duration
}

// The AI SDK's NoObjectGeneratedError and NoOutputGeneratedError texts.
const (
	errNoParse    = "No object generated: could not parse the response."
	errNoMatch    = "No object generated: response did not match schema."
	errNoOutput   = "No output generated."
	errTimeoutMsg = "The operation was aborted due to timeout"
)

// ParseReminder never fails: problems come back as ok false with an error text.
func ParseReminder(ctx context.Context, text, tz string, now int64, model models.LanguageModel, o ParseOptions) ParsedReminder {
	input := strx.Trim(text)
	if input == "" {
		return ParsedReminder{Text: "", TZ: tz, Error: "Nothing to parse."}
	}
	if err := AssertZone(tz); err != nil {
		return ParsedReminder{Text: input, TZ: tz, Error: err.Error()}
	}
	fail := func(msg string) ParsedReminder {
		return ParsedReminder{Text: input, TZ: tz, Error: "Couldn't read that reminder: " + msg}
	}
	out, err := generateObject(ctx, input, tz, now, model, o)
	if err != nil {
		return fail(err.Error())
	}
	res, err := FinishParse(out, input, tz, now)
	if err != nil {
		return fail(err.Error())
	}
	return res
}

func generateObject(ctx context.Context, input, tz string, now int64, model models.LanguageModel, o ParseOptions) (ModelReminder, error) {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	maxRetries := 1
	if o.MaxRetries != nil {
		maxRetries = *o.MaxRetries
	}
	delay := o.RetryDelay
	if delay == 0 {
		delay = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	temp := 0.0
	maxTokens := 400
	opts := models.CallOptions{
		Prompt:          []models.Message{models.System(BuildSystemPrompt(tz, now)), models.User(input)},
		Temperature:     &temp,
		MaxOutputTokens: &maxTokens,
		ResponseFormat:  &models.ResponseFormat{Type: "json", Schema: json.RawMessage(ParsedReminderSchema)},
	}
	var res models.GenerateResult
	var errs []error
	for {
		r, err := models.Generate(ctx, model, opts)
		if err == nil {
			res = r
			break
		}
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return ModelReminder{}, errors.New(errTimeoutMsg)
			}
			return ModelReminder{}, err
		}
		if maxRetries == 0 {
			return ModelReminder{}, err
		}
		errs = append(errs, err)
		try := len(errs)
		if try > maxRetries {
			return ModelReminder{}, fmt.Errorf("Failed after %d attempts. Last error: %s", try, err.Error())
		}
		var api *models.APICallError
		if !errors.As(err, &api) || !api.IsRetryable {
			if try == 1 {
				return ModelReminder{}, err
			}
			return ModelReminder{}, fmt.Errorf("Failed after %d attempts with non-retryable error: '%s'", try, err.Error())
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return ModelReminder{}, errors.New(errTimeoutMsg)
			}
			return ModelReminder{}, ctx.Err()
		}
		delay *= 2
	}
	text, finish := res.Text, res.FinishReason.Unified
	for _, c := range res.ToolCalls {
		if c.ToolName == models.JSONToolName {
			// The Anthropic JSON tool fallback: the call's input is the object.
			text, finish = c.Input, "stop"
			break
		}
	}
	if !(finish == "stop" || (finish != "tool-calls" && len(text) > 0)) {
		return ModelReminder{}, errors.New(errNoOutput)
	}
	return validateModelReminder(text)
}

func validateModelReminder(text string) (ModelReminder, error) {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return ModelReminder{}, errors.New(errNoParse)
	}
	o, ok := v.(map[string]any)
	if !ok {
		return ModelReminder{}, errors.New(errNoMatch)
	}
	t, ok1 := o["text"].(string)
	l, ok2 := o["localDateTime"].(string)
	rv, has := o["rrule"]
	if !ok1 || !ok2 || !has {
		return ModelReminder{}, errors.New(errNoMatch)
	}
	m := ModelReminder{Text: t, LocalDateTime: l}
	switch r := rv.(type) {
	case nil:
	case string:
		m.RRule = &r
	default:
		return ModelReminder{}, errors.New(errNoMatch)
	}
	return m, nil
}

// ParsedLocalStart is the localStart to store when creating a reminder from a
// parse result ("" when it failed).
func ParsedLocalStart(p ParsedReminder) string {
	if !p.OK || p.FireAt == nil {
		return ""
	}
	return UTCToLocal(*p.FireAt, p.TZ)
}
