package ics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/ics"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
)

// A feed in the shapes calendar services write: a timed event with a TZID,
// a UTC one, an all-day one, a weekly series with an EXDATE and a moved
// occurrence, a cancelled event, a declined invite, folded lines and escapes.
const feed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Google Inc//Google Calendar 70.9054//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:klinik@google.com\r\nDTSTART;TZID=Asia/Makassar:20261110T140000\r\nDTEND;TZID=Asia/Makassar:20261110T150000\r\nSUMMARY:Klinik\\, kontrol\r\nLOCATION:Jl. A. Yani km 4\\, Banjarmasin\r\nDESCRIPTION:Bawa kartu\\nantri 10 menit lebih awal. Join: https://meet.google.com/abc-defg-hij l\r\n" +
	" anjutan\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT10M\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:standup@x\r\nDTSTART:20261110T010000Z\r\nDTEND:20261110T011500Z\r\nRRULE:FREQ=DAILY;COUNT=5\r\nEXDATE:20261111T010000Z\r\nSUMMARY:Standup\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:standup@x\r\nRECURRENCE-ID:20261112T010000Z\r\nDTSTART:20261112T030000Z\r\nDTEND:20261112T031500Z\r\nSUMMARY:Standup (moved)\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:holiday@x\r\nDTSTART;VALUE=DATE:20261111\r\nDTEND;VALUE=DATE:20261112\r\nSUMMARY:Libur\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:gone@x\r\nDTSTART:20261110T060000Z\r\nDTEND:20261110T070000Z\r\nSTATUS:CANCELLED\r\nSUMMARY:Batal\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:nope@x\r\nDTSTART;TZID=SE Asia Standard Time:20261110T190000\r\nDURATION:PT2H\r\nSUMMARY:Webinar\r\nATTENDEE;CN=Owner;PARTSTAT=DECLINED:mailto:Owner@Example.com\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

var wita = time.FixedZone("WITA", 8*3600)

func TestEventsExpandRecurrenceOverridesAndZones(t *testing.T) {
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, wita)
	list, err := ics.Events(feed, from, from.AddDate(0, 0, 3), wita, []string{"owner@example.com"})
	tu.Must(t, err)
	var got []string
	for _, e := range list {
		got = append(got, e.Title+"@"+e.Start.In(wita).Format("01-02 15:04"))
	}
	want := []string{"Standup@11-10 09:00", "Klinik, kontrol@11-10 14:00", "Webinar@11-10 20:00", "Libur@11-11 00:00", "Standup (moved)@11-12 11:00"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	k := list[1]
	if k.Location != "Jl. A. Yani km 4, Banjarmasin" || !strings.Contains(k.Description, "kartu\nantri") || !strings.Contains(k.Description, "lanjutan") || k.URL != "https://meet.google.com/abc-defg-hij" {
		t.Fatalf("%+v", k)
	}
	if !list[2].Declined || list[2].End.Sub(list[2].Start) != 2*time.Hour {
		t.Fatalf("declined, with a duration: %+v", list[2])
	}
	if !list[3].AllDay || !list[0].Recurring || list[0].ID == list[4].ID {
		t.Fatalf("%+v", list)
	}
	if _, err := ics.Events("<html>nope</html>", from, from, wita, nil); err != ics.ErrNotCalendar {
		t.Fatalf("%v", err)
	}
}

func server(t *testing.T, status int, body string, hits *int32) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "text/calendar")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/private-abc123/basic.ics"
}

func ctxWith(t *testing.T, link, emails string) connectors.Context {
	t.Helper()
	cfg := jsonx.NewMap[any]()
	if emails != "" {
		cfg.Set("emails", emails)
	}
	return conntest.Ctx(t, nil, "ics-1", cfg, map[string]string{"url": link})
}

func TestGetEventsReadsTheFeedAndCachesIt(t *testing.T) {
	var hits int32
	link := server(t, 200, feed, &hits)
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, wita).UnixMilli()
	rt := ics.New(ics.Options{Clock: func() int64 { return now }})
	c := ctxWith(t, link, "owner@example.com")
	running, err := rt.Start(c)
	tu.Must(t, err)
	set, err := running.Tools(context.Background(), connectors.TurnInfo{TZ: "Asia/Makassar", Now: now})
	tu.Must(t, err)
	pt, _ := set.Get("get_events")
	if pt.Class != "read" || !pt.Untrusted {
		t.Fatalf("%+v", pt)
	}
	out := conntest.Exec(t, pt.Tool, `{"days":1}`)
	if out.N("count") != 3 || out.S("events.1.title") != "Klinik, kontrol" || out.S("events.1.start") != "2026-11-10T14:00:00+08:00" || out.S("events.1.link") != "https://meet.google.com/abc-defg-hij" || !out.B("events.2.declined") {
		t.Fatalf("%s", out)
	}
	out = conntest.Exec(t, pt.Tool, `{"from":"2026-11-11","to":"2026-11-12"}`)
	if out.N("count") != 1 || out.S("events.0.start") != "2026-11-11" || !out.B("events.0.allDay") {
		t.Fatalf("%s", out)
	}
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Fatalf("the feed is cached: %d fetches", h)
	}
	if out := conntest.Exec(t, pt.Tool, `{"from":"tomorrow"}`); out.B("ok") != false || !strings.Contains(out.S("error"), "from isn't a date") {
		t.Fatalf("%s", out)
	}
}

func TestTestSaysWhatsWrongWithoutLeakingTheLink(t *testing.T) {
	var hits int32
	link := server(t, 404, "gone", &hits)
	res, err := ics.New(ics.Options{}).Test(ctxWith(t, link, ""))
	tu.Must(t, err)
	if res.OK || !strings.Contains(res.Detail, "404") || strings.Contains(res.Detail, "private-abc123") {
		t.Fatalf("%+v", res)
	}
	page := server(t, 200, "<html>calendar</html>", &hits)
	res, err = ics.New(ics.Options{}).Test(ctxWith(t, page, ""))
	tu.Must(t, err)
	if res.OK || !strings.Contains(res.Detail, "BEGIN:VCALENDAR") {
		t.Fatalf("%+v", res)
	}
	ok := server(t, 200, feed, &hits)
	res, err = ics.New(ics.Options{Clock: func() int64 { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC).UnixMilli() }}).Test(ctxWith(t, ok, ""))
	tu.Must(t, err)
	if !res.OK || !strings.Contains(res.Detail, "events in the next 7 days") {
		t.Fatalf("%+v", res)
	}
	if res, _ := ics.New(ics.Options{}).Test(ctxWith(t, "", "")); res.OK {
		t.Fatal("no link")
	}
}

func TestWebcalLinksAreFetchedOverHTTPS(t *testing.T) {
	f := ics.NewFeed("webcal://p01-calendars.icloud.com/published/2/abc", nil, ics.Options{})
	if f.URL != "https://p01-calendars.icloud.com/published/2/abc" {
		t.Fatal(f.URL)
	}
}
