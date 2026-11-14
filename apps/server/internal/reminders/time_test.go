package reminders_test

// Port of packages/core/src/reminders/time.test.ts.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/teambition/rrule-go"
	"github.com/zakiego/palui/apps/server/internal/reminders"
	"github.com/zakiego/palui/apps/server/internal/zone"
)

const (
	NY   = "America/New_York"
	WITA = "Asia/Makassar"
)

// Tuesday 2026-11-10 10:00 WITA.
var NOW = time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC).UnixMilli()

func ms(iso string) int64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

func l2u(t *testing.T, local, tz string) int64 {
	t.Helper()
	v, err := reminders.LocalToUTC(local, tz)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func rr(s string) *string { return &s }

func isTimeErr(err error) bool {
	var te *reminders.TimeError
	return errors.As(err, &te)
}

func occ(t *testing.T, s reminders.Schedule, after int64, n int, f func(int64) string) []string {
	t.Helper()
	out, err := reminders.OccurrencesAfter(s, after, n)
	if err != nil {
		t.Fatal(err)
	}
	strs := make([]string, len(out))
	for i, v := range out {
		strs[i] = f(v)
	}
	return strs
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestLocalToUTC(t *testing.T) {
	// 2026-03-08 02:30 doesn't exist in New York; clocks jump 02:00 -> 03:00.
	if got := reminders.UTCToISO(l2u(t, "2026-03-08T02:30", NY), NY); got != "2026-03-08T03:30:00.000-04:00" {
		t.Fatal(got)
	}
	// 2026-11-01 01:30 happens twice; the first one is EDT (-04:00).
	if got := l2u(t, "2026-11-01T01:30", NY); got != ms("2026-11-01T05:30:00Z") {
		t.Fatal(got)
	}
	if got := l2u(t, "2026-11-11T09:00", WITA); got != ms("2026-11-11T01:00:00Z") {
		t.Fatal(got)
	}
	if got := reminders.UTCToLocal(ms("2026-11-11T01:00:00Z"), WITA); got != "2026-11-11T09:00:00" {
		t.Fatal(got)
	}
	if _, err := reminders.ParseLocal("2026-11-11T09:00+08:00"); !isTimeErr(err) {
		t.Fatal(err)
	}
	if _, err := reminders.ParseLocal("2026-11-31T09:00"); !isTimeErr(err) {
		t.Fatal(err)
	}
	if _, err := reminders.LocalToUTC("2026-11-11T09:00", "Mars/Olympus"); !isTimeErr(err) {
		t.Fatal(err)
	}
}

func TestRecurrenceInWallClockTime(t *testing.T) {
	iso := func(tz string) func(int64) string { return func(v int64) string { return reminders.UTCToISO(v, tz) } }
	local := func(tz string) func(int64) string { return func(v int64) string { return reminders.UTCToLocal(v, tz) } }

	t.Run("daily 02:30 across spring-forward", func(t *testing.T) {
		s := reminders.Schedule{RRule: rr("FREQ=DAILY"), TZ: NY, LocalStart: "2026-03-06T02:30"}
		eq(t, occ(t, s, l2u(t, "2026-03-06T12:00", NY), 3, iso(NY)), []string{"2026-03-07T02:30:00.000-05:00", "2026-03-08T03:30:00.000-04:00", "2026-03-09T02:30:00.000-04:00"})
	})
	t.Run("daily 01:30 once on fall-back day", func(t *testing.T) {
		s := reminders.Schedule{RRule: rr("FREQ=DAILY"), TZ: NY, LocalStart: "2026-10-30T01:30"}
		eq(t, occ(t, s, l2u(t, "2026-10-31T12:00", NY), 2, iso(NY)), []string{"2026-11-01T01:30:00.000-04:00", "2026-11-02T01:30:00.000-05:00"})
	})
	t.Run("weekly keeps 08:00 after DST", func(t *testing.T) {
		s := reminders.Schedule{RRule: rr("FREQ=WEEKLY;BYDAY=MO"), TZ: NY, LocalStart: "2026-10-26T08:00"}
		eq(t, occ(t, s, l2u(t, "2026-10-26T09:00", NY), 2, iso(NY)), []string{"2026-11-02T08:00:00.000-05:00", "2026-11-09T08:00:00.000-05:00"})
	})
	t.Run("last Friday", func(t *testing.T) {
		s := reminders.Schedule{RRule: rr("FREQ=MONTHLY;BYDAY=-1FR"), TZ: WITA, LocalStart: "2026-11-10T17:00"}
		eq(t, occ(t, s, NOW, 4, local(WITA)), []string{"2026-11-27T17:00:00", "2026-12-25T17:00:00", "2027-01-29T17:00:00", "2027-02-26T17:00:00"})
	})
	t.Run("skips months without a 31st", func(t *testing.T) {
		s := reminders.Schedule{RRule: rr("FREQ=MONTHLY;BYMONTHDAY=31"), TZ: WITA, LocalStart: "2026-10-31T09:00"}
		eq(t, occ(t, s, NOW, 3, local(WITA)), []string{"2026-12-31T09:00:00", "2027-01-31T09:00:00", "2027-03-31T09:00:00"})
	})
	t.Run("BYMONTHDAY=-1", func(t *testing.T) {
		s := reminders.Schedule{RRule: rr("FREQ=MONTHLY;BYMONTHDAY=-1"), TZ: WITA, LocalStart: "2026-11-10T09:00"}
		eq(t, occ(t, s, NOW, 3, local(WITA)), []string{"2026-11-30T09:00:00", "2026-12-31T09:00:00", "2027-01-31T09:00:00"})
	})
	t.Run("anchor not counted unless it matches", func(t *testing.T) {
		s := reminders.Schedule{RRule: rr("FREQ=WEEKLY;BYDAY=MO"), TZ: WITA, LocalStart: "2026-11-10T08:00"}
		v, _, _ := reminders.NextOccurrence(s, NOW)
		if reminders.UTCToLocal(v, WITA) != "2026-11-16T08:00:00" {
			t.Fatal(v)
		}
	})
	t.Run("COUNT and UNTIL end a series", func(t *testing.T) {
		for _, r := range []string{"FREQ=DAILY;COUNT=2", "FREQ=DAILY;UNTIL=20261109T000000Z"} {
			if _, ok, err := reminders.NextOccurrence(reminders.Schedule{RRule: rr(r), TZ: WITA, LocalStart: "2026-11-01T09:00"}, NOW); ok || err != nil {
				t.Fatal(r, err)
			}
		}
	})
	t.Run("one-off only when ahead", func(t *testing.T) {
		if _, ok, _ := reminders.NextOccurrence(reminders.Schedule{TZ: WITA, LocalStart: "2026-11-10T09:00"}, NOW); ok {
			t.Fatal("past one-off")
		}
		if v, _, _ := reminders.NextOccurrence(reminders.Schedule{TZ: WITA, LocalStart: "2026-11-10T11:00"}, NOW); v != NOW+3600_000 {
			t.Fatal(v)
		}
	})
	t.Run("fast-forward matches plain rrule", func(t *testing.T) {
		cases := [][2]string{
			{"FREQ=HOURLY;INTERVAL=7", "2016-01-01T00:00:00"},
			{"FREQ=MINUTELY;INTERVAL=45", "2024-03-01T00:10:00"},
			{"FREQ=WEEKLY;INTERVAL=3;BYDAY=SA,TU", "2019-05-04T09:00:00"},
			{"FREQ=DAILY;INTERVAL=5;BYHOUR=8,20;BYMINUTE=0", "2021-02-03T08:00:00"},
		}
		for _, c := range cases {
			ours := occ(t, reminders.Schedule{RRule: rr(c[0]), TZ: WITA, LocalStart: c[1]}, NOW, 5, local(WITA))
			opt, err := rrule.StrToROption(c[0])
			if err != nil {
				t.Fatal(err)
			}
			start, _ := time.Parse("2006-01-02T15:04:05", c[1])
			opt.Dtstart = start
			r, err := rrule.NewRRule(*opt)
			if err != nil {
				t.Fatal(err)
			}
			var plain []string
			for _, d := range r.Between(ms2t("2026-11-10T10:00:00Z"), ms2t("2027-12-31T00:00:00Z"), false) {
				if len(plain) < 5 {
					plain = append(plain, d.Format("2006-01-02T15:04:05"))
				}
			}
			eq(t, ours, plain)
		}
	})
	t.Run("fast for a dense rule anchored long ago", func(t *testing.T) {
		s := reminders.Schedule{RRule: rr("FREQ=MINUTELY;INTERVAL=5"), TZ: WITA, LocalStart: "2025-01-01T00:00"}
		t0 := time.Now()
		if v, _, _ := reminders.NextOccurrence(s, NOW); v != NOW+5*60_000 {
			t.Fatal(v)
		}
		if time.Since(t0) > 3*time.Second {
			t.Fatal("slow")
		}
	})
}

func ms2t(iso string) time.Time { return time.UnixMilli(ms(iso)).UTC() }

func TestNormalizeRrule(t *testing.T) {
	norm := func(s string) string {
		t.Helper()
		v, err := reminders.NormalizeRrule(s)
		if err != nil {
			t.Fatal(s, err)
		}
		return v
	}
	if v := norm("RRULE:byday=we,mo;freq=weekly;interval=1"); v != "FREQ=WEEKLY;BYDAY=MO,WE" {
		t.Fatal(v)
	}
	if v := norm("BYDAY=+1MO;FREQ=MONTHLY;WKST=MO"); v != "FREQ=MONTHLY;BYDAY=1MO" {
		t.Fatal(v)
	}
	for _, bad := range []string{"FREQ=BOGUS", "DTSTART:20261110T090000\nRRULE:FREQ=DAILY", "FREQ=SECONDLY", "FREQ=MINUTELY", "FREQ=DAILY;FOO=1", ""} {
		if _, err := reminders.NormalizeRrule(bad); !isTimeErr(err) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	everyMinute := make([]string, 60)
	for i := range everyMinute {
		everyMinute[i] = string(rune('0'+i/10)) + string(rune('0'+i%10))
	}
	for _, bad := range []string{"FREQ=DAILY;BYSECOND=0,1,2,3,4,5,6,7,8,9,10", "FREQ=HOURLY;BYMINUTE=" + strings.Join(everyMinute, ","), "FREQ=DAILY;BYHOUR=8;BYMINUTE=0,2"} {
		if _, err := reminders.NormalizeRrule(bad); err == nil || !strings.Contains(err.Error(), "every 5 minutes") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if v := norm("FREQ=DAILY;BYHOUR=8;BYMINUTE=0,5"); v != "FREQ=DAILY;BYHOUR=8;BYMINUTE=0,5" {
		t.Fatal(v)
	}
	if v := norm("FREQ=MINUTELY;INTERVAL=5"); v != "FREQ=MINUTELY;INTERVAL=5" {
		t.Fatal(v)
	}

	t0 := time.Now()
	for _, never := range []string{"FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=30", "FREQ=MINUTELY;INTERVAL=5;BYMONTH=4;BYMONTHDAY=31"} {
		if _, err := reminders.NormalizeRrule(never); err == nil || !strings.Contains(err.Error(), "never fires") {
			t.Fatal(never, err)
		}
	}
	if time.Since(t0) > 500*time.Millisecond {
		t.Fatal("never-fires check is slow")
	}

	if v := norm("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29"); v != "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29" {
		t.Fatal(v)
	}
	t0 = time.Now()
	v, _, _ := reminders.NextOccurrence(reminders.Schedule{RRule: rr("FREQ=HOURLY;BYMONTH=2;BYMONTHDAY=29"), TZ: WITA, LocalStart: "2026-11-10T00:00"}, NOW)
	if reminders.UTCToLocal(v, WITA) != "2028-02-29T00:00:00" || time.Since(t0) > 500*time.Millisecond {
		t.Fatal(v, time.Since(t0))
	}

	if _, ok, err := reminders.NextOccurrence(reminders.Schedule{RRule: rr("FREQ=DAILY;UNTIL=20200101T000000"), TZ: WITA, LocalStart: "2019-12-30T08:00"}, NOW); ok || err != nil {
		t.Fatal(ok, err)
	}
}

func TestEchoFormatting(t *testing.T) {
	when := func(local, tz string) string { return reminders.FormatWhen(l2u(t, local, tz), tz, NOW) }
	for _, c := range [][2]string{
		{when("2026-11-10T14:30", WITA), "Hari ini, 14.30 WITA"},
		{when("2026-11-11T09:00", WITA), "Besok, Rabu 11 Nov, 09.00 WITA"},
		{when("2026-11-16T08:00", WITA), "Senin 16 Nov, 08.00 WITA"},
		{when("2027-01-01T09:00", WITA), "Jumat 1 Jan 2027, 09.00 WITA"},
		{reminders.ZoneLabel("Asia/Jakarta", NOW), "WIB"},
		{reminders.ZoneLabel("Asia/Makassar", NOW), "WITA"},
		{reminders.ZoneLabel("Asia/Jayapura", NOW), "WIT"},
		// NOW is Monday 21:00 in New York, so Tuesday is "Besok" there.
		{when("2026-11-10T09:00", NY), "Besok, Selasa 10 Nov, 09.00 EST"},
	} {
		if c[0] != c[1] {
			t.Errorf("%q, want %q", c[0], c[1])
		}
	}

	d := func(r string, localStart string) string {
		v, err := reminders.DescribeRecurrence(reminders.Schedule{RRule: &r, TZ: WITA, LocalStart: localStart}, NOW)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	const def = "2026-11-16T08:00"
	for _, c := range [][2]string{
		{d("FREQ=WEEKLY;BYDAY=MO", def), "Tiap Senin, 08.00 WITA"},
		{d("FREQ=WEEKLY", def), "Tiap Senin, 08.00 WITA"},
		{d("FREQ=DAILY", "2026-11-11T07:00"), "Tiap hari, 07.00 WITA"},
		{d("FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", def), "Tiap hari kerja, 08.00 WITA"},
		{d("FREQ=WEEKLY;BYDAY=MO,WE", def), "Tiap Senin dan Rabu, 08.00 WITA"},
		{d("FREQ=WEEKLY;INTERVAL=2;BYDAY=FR", def), "Tiap 2 minggu, Jumat, 08.00 WITA"},
		{d("FREQ=MONTHLY;BYDAY=-1FR", "2026-11-27T17:00"), "Tiap Jumat terakhir, 17.00 WITA"},
		{d("FREQ=MONTHLY;BYMONTHDAY=1", "2026-12-01T09:00"), "Tiap tanggal 1, 09.00 WITA"},
		{d("FREQ=MONTHLY;BYMONTHDAY=-1", "2026-11-30T09:00"), "Tiap akhir bulan, 09.00 WITA"},
		{d("FREQ=YEARLY;BYMONTH=8;BYMONTHDAY=17", "2027-08-17T09:00"), "Tiap 17 Agu, 09.00 WITA"},
		{d("FREQ=HOURLY;INTERVAL=2", def), "Tiap 2 jam"},
		{d("FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0", def), "Tiap hari, 08.00 dan 20.00 WITA"},
	} {
		if c[0] != c[1] {
			t.Errorf("%q, want %q", c[0], c[1])
		}
	}

	at := l2u(t, "2026-11-11T09:00", WITA)
	if v, _ := reminders.FormatEcho(reminders.Schedule{TZ: WITA, LocalStart: "2026-11-11T09:00"}, at, NOW); v != "Besok, Rabu 11 Nov, 09.00 WITA" {
		t.Fatal(v)
	}
	if v, _ := reminders.FormatEcho(reminders.Schedule{RRule: rr("FREQ=DAILY"), TZ: WITA, LocalStart: "2026-11-11T09:00"}, at, NOW); v != "Tiap hari, 09.00 WITA" {
		t.Fatal(v)
	}
}

func TestTomorrowAt(t *testing.T) {
	// 23:30 WITA on Tuesday -> Wednesday 09:00 WITA.
	if v := reminders.UTCToLocal(reminders.TomorrowAt(l2u(t, "2026-11-10T23:30", WITA), WITA, 9, 0), WITA); v != "2026-11-11T09:00:00" {
		t.Fatal(v)
	}
	// 00:30 WITA Wednesday is still Tuesday in UTC; tomorrow is Thursday.
	if v := reminders.UTCToLocal(reminders.TomorrowAt(l2u(t, "2026-11-11T00:30", WITA), WITA, 9, 0), WITA); v != "2026-11-12T09:00:00" {
		t.Fatal(v)
	}
}

func TestScheduleSignal(t *testing.T) {
	n := 0
	off := reminders.OnScheduleChanged(func() { n++ })
	reminders.OnScheduleChanged(func() { panic("listener bug") })
	reminders.NotifyScheduleChanged()
	off()
	reminders.NotifyScheduleChanged()
	if n != 1 {
		t.Fatal(n)
	}
}

func TestZoneNamesLikeIntl(t *testing.T) {
	for name, want := range map[string]bool{"asia/makassar": true, "+08:00": true, "+0800": true, "-05:30": true, "UTC": true, "Etc/GMT+8": true, "Asia/Makassar ": false, "UTC+8": false, "+24:00": false, "Local": false, "": false, "Factory": false} {
		if zone.Valid(name) != want {
			t.Errorf("Valid(%q) != %v", name, want)
		}
	}
	// Budget's day rollover follows luxon in a DST gap at midnight
	// (Santiago springs forward 00:00 -> 01:00 on 2026-09-06).
	loc := zone.MustLoad("America/Santiago")
	before := l2u(t, "2026-09-05T12:00", "America/Santiago")
	if got := reminders.UTCToISO(zone.NextDay(before, loc), "America/Santiago"); got != "2026-09-06T01:00:00.000-03:00" {
		t.Fatal(got)
	}
}
