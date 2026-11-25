package watches_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/db"
	tu "github.com/zakiego/palui/apps/server/internal/testutil"
	"github.com/zakiego/palui/apps/server/internal/watches"
)

var (
	NOW  = tu.NOW
	MIN  = tu.MIN
	HOUR = tu.HOUR
)

func create(t *testing.T, q db.Q, chatID string, hours int64, now int64) db.Watch {
	t.Helper()
	w, err := watches.Create(q, watches.CreateInput{
		ChatID: chatID, CreatedBy: "Bayu", Topic: "Laba resmi Danantara",
		Question: "Danantara's audited 2025 net profit", DoneWhen: "An audited figure from Danantara or BPK. The Rp326 T claim doesn't count.",
		ExpiresAt: now + hours*HOUR,
	}, now)
	tu.Must(t, err)
	return w
}

func get(t *testing.T, q db.Q, id string) db.Watch {
	t.Helper()
	w, ok, err := watches.Get(q, id)
	tu.Must(t, err)
	if !ok {
		t.Fatalf("no watch %s", id)
	}
	return w
}

func count(t *testing.T, q db.Q, query string, args ...any) int {
	t.Helper()
	var n int
	tu.Must(t, q.QueryRow(query, args...).Scan(&n))
	return n
}

func outboxTexts(t *testing.T, q db.Q, chatID string) []string {
	t.Helper()
	rows, err := q.Query("SELECT json_extract(payload, '$.text') FROM outbox WHERE chat_id = ? ORDER BY created_at, id", chatID)
	tu.Must(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		tu.Must(t, rows.Scan(&s))
		out = append(out, s)
	}
	return out
}

type fake struct {
	answers []string
	status  string
	err     error
	calls   []watches.CheckRequest
}

func (f *fake) check(r watches.CheckRequest) (watches.CheckResponse, error) {
	f.calls = append(f.calls, r)
	if f.err != nil {
		return watches.CheckResponse{}, f.err
	}
	status := f.status
	if status == "" {
		status = "ok"
	}
	text := ""
	if len(f.answers) > 0 {
		text, f.answers = f.answers[0], f.answers[1:]
	}
	return watches.CheckResponse{RunID: "", Text: text, Status: status}, nil
}

func engine(q db.Q, f *fake, clock *int64) *watches.Engine {
	return watches.NewEngine(watches.Options{DB: q, Clock: func() int64 { return *clock }, Check: f.check, Serial: true})
}

const notMet = `{"met": false, "notes": "Only the Rp326 T claim so far.", "summary": "Belum ada angka teraudit."}`
const met = `{"met": true, "message": "Laba bersih Danantara 2025 yang udah diaudit: Rp301 T.", "sources": ["https://example.com/danantara-audit"], "notes": "found", "summary": "Ketemu."}`

func TestCreate(t *testing.T) {
	t.Run("first check one interval out, never past the end", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w := create(t, d, chatID, 24, NOW)
		if w.Status != watches.StatusActive || w.EveryMin != watches.DefaultEveryMin || *w.NextCheckAt != NOW+30*MIN || w.TokenCap != watches.TokenCap {
			t.Fatalf("%+v", w)
		}
		short, err := watches.Create(d, watches.CreateInput{ChatID: chatID, Topic: "x", Question: "q", DoneWhen: "d", EveryMin: 60, ExpiresAt: NOW + 20*MIN}, NOW)
		tu.Must(t, err)
		if *short.NextCheckAt != NOW+20*MIN {
			t.Fatal(*short.NextCheckAt)
		}
	})
	t.Run("refuses bad input and an 11th watch in one chat", func(t *testing.T) {
		d, chatID, otherChat := tu.Setup(t)
		bad := []watches.CreateInput{
			{ChatID: chatID, Topic: "", Question: "q", DoneWhen: "d", ExpiresAt: NOW + HOUR},
			{ChatID: chatID, Topic: "t", Question: "q", DoneWhen: "", ExpiresAt: NOW + HOUR},
			{ChatID: chatID, Topic: "t", Question: "q", DoneWhen: "d", EveryMin: 5, ExpiresAt: NOW + HOUR},
			{ChatID: chatID, Topic: "t", Question: "q", DoneWhen: "d", ExpiresAt: NOW - 1},
			{ChatID: chatID, Topic: "t", Question: "q", DoneWhen: "d", ExpiresAt: NOW + 15*24*HOUR},
		}
		for i, in := range bad {
			var we *watches.WatchError
			if _, err := watches.Create(d, in, NOW); !errors.As(err, &we) {
				t.Fatalf("case %d: %v", i, err)
			}
		}
		for i := 0; i < watches.MaxActivePerChat; i++ {
			create(t, d, chatID, 24, NOW)
		}
		var we *watches.WatchError
		if _, err := watches.Create(d, watches.CreateInput{ChatID: chatID, Topic: "t", Question: "q", DoneWhen: "d", ExpiresAt: NOW + HOUR}, NOW); !errors.As(err, &we) || !strings.Contains(we.Msg, "10 active") {
			t.Fatal(err)
		}
		create(t, d, otherChat, 24, NOW)
	})
}

func TestParseVerdict(t *testing.T) {
	v, err := watches.ParseVerdict("Here it is:\n```json\n" + met + "\n```")
	tu.Must(t, err)
	if !v.Met || len(v.Sources) != 1 {
		t.Fatalf("%+v", v)
	}
	// Found, but nothing to show for it: not met.
	v, err = watches.ParseVerdict(`{"met": true, "message": "Rp301 T", "sources": ["not a url"]}`)
	tu.Must(t, err)
	if v.Met {
		t.Fatal("met without a source")
	}
	if _, err := watches.ParseVerdict("NOTHING"); err == nil {
		t.Fatal("no JSON should fail")
	}
}

func TestEngine(t *testing.T) {
	t.Run("not met: stays quiet, keeps its notes, checks again later", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w := create(t, d, chatID, 24, NOW)
		clock := NOW + 30*MIN
		f := &fake{answers: []string{notMet}}
		e := engine(d, f, &clock)
		if _, err := e.Sweep(clock); err != nil {
			t.Fatal(err)
		}
		got := get(t, d, w.ID)
		if got.Status != watches.StatusActive || got.Checks != 1 || got.Notes != "Only the Rp326 T claim so far." || *got.NextCheckAt != clock+30*MIN {
			t.Fatalf("%+v", got)
		}
		if len(outboxTexts(t, d, chatID)) != 0 {
			t.Fatal("sent something")
		}
		if len(f.calls) != 1 || !strings.Contains(f.calls[0].Prompt, "The Rp326 T claim doesn't count.") || !strings.Contains(f.calls[0].Prompt, "first check") {
			t.Fatal(f.calls)
		}
		// The next check carries the notes forward.
		clock += 30 * MIN
		f.answers = []string{notMet}
		_, _ = e.Sweep(clock)
		if !strings.Contains(f.calls[1].Prompt, "Only the Rp326 T claim so far.") {
			t.Fatal(f.calls[1].Prompt)
		}
	})
	t.Run("met: posts once with the source and is done", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w := create(t, d, chatID, 24, NOW)
		clock := NOW + 30*MIN
		f := &fake{answers: []string{met}}
		e := engine(d, f, &clock)
		_, _ = e.Sweep(clock)
		got := get(t, d, w.ID)
		if got.Status != watches.StatusDone || got.OutboxID == nil || got.EndedAt == nil {
			t.Fatalf("%+v", got)
		}
		texts := outboxTexts(t, d, chatID)
		if len(texts) != 1 || !strings.Contains(texts[0], "Rp301 T") || !strings.Contains(texts[0], "https://example.com/danantara-audit") || !strings.Contains(texts[0], "Pantauan: Laba resmi Danantara") {
			t.Fatal(texts)
		}
		if n := count(t, d, "SELECT count(*) FROM messages WHERE chat_id = ? AND role = 'assistant'", chatID); n != 1 {
			t.Fatal(n)
		}
		clock += 2 * HOUR
		_, _ = e.Sweep(clock)
		if len(f.calls) != 1 || len(outboxTexts(t, d, chatID)) != 1 {
			t.Fatal("checked or sent again")
		}
	})
	t.Run("last check not met: expires with one notice", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w, err := watches.Create(d, watches.CreateInput{ChatID: chatID, Topic: "Laba", Question: "q", DoneWhen: "d", EveryMin: 60, ExpiresAt: NOW + 90*MIN}, NOW)
		tu.Must(t, err)
		clock := NOW + 60*MIN
		f := &fake{answers: []string{notMet, notMet}}
		e := engine(d, f, &clock)
		_, _ = e.Sweep(clock)
		if got := get(t, d, w.ID); got.Status != watches.StatusActive || *got.NextCheckAt != NOW+90*MIN {
			t.Fatalf("%+v", got)
		}
		clock = NOW + 90*MIN
		_, _ = e.Sweep(clock)
		if !strings.Contains(f.calls[1].Prompt, "LAST check") {
			t.Fatal("last check not marked")
		}
		got := get(t, d, w.ID)
		texts := outboxTexts(t, d, chatID)
		if got.Status != watches.StatusExpired || len(texts) != 1 || !strings.Contains(texts[0], "Pantauan selesai: Laba") || !strings.Contains(texts[0], "Belum ada angka teraudit.") {
			t.Fatalf("%s %v", got.Status, texts)
		}
	})
	t.Run("cancelled while checking: sends nothing", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w := create(t, d, chatID, 24, NOW)
		clock := NOW + 30*MIN
		check := func(r watches.CheckRequest) (watches.CheckResponse, error) {
			_, err := watches.Cancel(d, w.ID, clock)
			tu.Must(t, err)
			return watches.CheckResponse{Text: met, Status: "ok"}, nil
		}
		e := watches.NewEngine(watches.Options{DB: d, Clock: func() int64 { return clock }, Check: check, Serial: true})
		_, _ = e.Sweep(clock)
		if got := get(t, d, w.ID); got.Status != watches.StatusCancelled || len(outboxTexts(t, d, chatID)) != 0 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("five failures in a row stop it with a notice; a budget skip isn't one", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w := create(t, d, chatID, 24, NOW)
		clock := NOW
		f := &fake{status: "budget"}
		e := engine(d, f, &clock)
		clock += 30 * MIN
		_, _ = e.Sweep(clock)
		if got := get(t, d, w.ID); got.Fails != 0 || got.Status != watches.StatusActive {
			t.Fatalf("%+v", got)
		}
		f.status, f.err = "", errors.New("model down")
		for i := 0; i < watches.MaxFails; i++ {
			clock += 30 * MIN
			_, _ = e.Sweep(clock)
		}
		got := get(t, d, w.ID)
		texts := outboxTexts(t, d, chatID)
		if got.Status != watches.StatusFailed || len(texts) != 1 || !strings.Contains(texts[0], "gagal ngecek 5 kali") {
			t.Fatalf("%s %v", got.Status, texts)
		}
	})
	t.Run("found long past its end after downtime: expires without a check", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w := create(t, d, chatID, 2, NOW)
		clock := NOW + 5*HOUR
		f := &fake{}
		e := engine(d, f, &clock)
		_, _ = e.Sweep(clock)
		if len(f.calls) != 0 {
			t.Fatal("checked")
		}
		if got := get(t, d, w.ID); got.Status != watches.StatusExpired || len(outboxTexts(t, d, chatID)) != 1 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("recover puts a claimed check back in line", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w := create(t, d, chatID, 24, NOW)
		_, ok, err := watches.Claim(d, NOW+30*MIN)
		tu.Must(t, err)
		if !ok {
			t.Fatal("not claimed")
		}
		tu.Must(t, watches.Recover(d, NOW+31*MIN))
		if got := get(t, d, w.ID); got.Status != watches.StatusActive || got.LeaseUntil != nil {
			t.Fatalf("%+v", got)
		}
	})
}

func TestStartsAt(t *testing.T) {
	in := func(chatID string, starts int64) watches.CreateInput {
		return watches.CreateInput{ChatID: chatID, Topic: "Closing BBCA", Question: "BBCA closing price today", DoneWhen: "The final closing price for today.", EveryMin: 15, ExpiresAt: NOW + 30*HOUR, StartsAt: starts}
	}
	t.Run("no check before the start, the first one right at it", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w, err := watches.Create(d, in(chatID, NOW+22*HOUR), NOW)
		tu.Must(t, err)
		if w.StartsAt == nil || *w.StartsAt != NOW+22*HOUR || *w.NextCheckAt != NOW+22*HOUR {
			t.Fatalf("%+v", w)
		}
		clock := NOW
		f := &fake{answers: []string{notMet, notMet}}
		e := engine(d, f, &clock)
		// Every 15 minutes up to a minute before the start: nothing runs.
		for clock = NOW + 15*MIN; clock < NOW+22*HOUR; clock += 15 * MIN {
			_, _ = e.Sweep(clock)
		}
		_, _ = e.Sweep(NOW + 22*HOUR - 1)
		if len(f.calls) != 0 || get(t, d, w.ID).Checks != 0 {
			t.Fatalf("checked %d times before the start", len(f.calls))
		}
		if due, ok, err := watches.NextDue(d); err != nil || !ok || due != NOW+22*HOUR {
			t.Fatal(due, ok, err)
		}
		clock = NOW + 22*HOUR
		_, _ = e.Sweep(clock)
		got := get(t, d, w.ID)
		if len(f.calls) != 1 || got.Checks != 1 || *got.NextCheckAt != clock+15*MIN {
			t.Fatalf("%d calls, %+v", len(f.calls), got)
		}
		if !strings.Contains(f.calls[0].Prompt, "first check") {
			t.Fatal(f.calls[0].Prompt)
		}
	})
	t.Run("refuses a start that passed or isn't before the end", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		for _, c := range []struct {
			starts int64
			msg    string
		}{
			{NOW, "already passed"},
			{NOW - HOUR, "already passed"},
			{NOW + 30*HOUR, "before the end"},
			{NOW + 31*HOUR, "before the end"},
		} {
			var we *watches.WatchError
			if _, err := watches.Create(d, in(chatID, c.starts), NOW); !errors.As(err, &we) || !strings.Contains(we.Msg, c.msg) {
				t.Fatalf("start %d: %v", c.starts-NOW, err)
			}
		}
		if n := count(t, d, "SELECT count(*) FROM watches"); n != 0 {
			t.Fatal(n)
		}
	})
	t.Run("a watch without a start begins now as before", func(t *testing.T) {
		d, chatID, _ := tu.Setup(t)
		w, err := watches.Create(d, in(chatID, 0), NOW)
		tu.Must(t, err)
		if w.StartsAt != nil || *w.NextCheckAt != NOW+15*MIN {
			t.Fatalf("%+v", w)
		}
	})
}
