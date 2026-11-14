package httpapi_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/auth"
	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/events"
	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// events-stream.test.ts and stream-guard.test.ts, against a real listener.

type sse struct {
	t      *testing.T
	res    *http.Response
	r      *bufio.Reader
	cancel context.CancelFunc
}

func openSSE(t *testing.T, e *env, path string, opts ...reqOpt) *sse {
	t.Helper()
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
	for _, o := range opts {
		o(req)
	}
	res, err := http.DefaultClient.Do(req)
	testutil.Must(t, err)
	t.Cleanup(func() { cancel(); res.Body.Close() })
	return &sse{t: t, res: res, r: bufio.NewReader(res.Body), cancel: cancel}
}

// next reads one frame (up to the blank line), or "" at the end of the stream.
func (s *sse) next() string {
	s.t.Helper()
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		for {
			line, err := s.r.ReadString('\n')
			b.WriteString(line)
			if err != nil {
				done <- b.String()
				return
			}
			if line == "\n" {
				done <- b.String()
				return
			}
		}
	}()
	select {
	case f := <-done:
		return f
	case <-time.After(5 * time.Second):
		s.t.Fatal("no frame within 5 s")
		return ""
	}
}

// nextData skips keepalive comments.
func (s *sse) nextData() string {
	s.t.Helper()
	for {
		f := s.next()
		if !strings.HasPrefix(f, ":") {
			return f
		}
	}
}

func logEvent(t *testing.T, e *env, kind, message string, connectorID *string) int64 {
	t.Helper()
	ev, err := events.Log(e.d, events.Input{Kind: kind, Message: message, ConnectorID: connectorID}, e.now.Load())
	testutil.Must(t, err)
	return ev.ID
}

func TestEventPollingHelpers(t *testing.T) {
	e := newEnv(t, nil)
	a := logEvent(t, e, "a", "one", nil)
	logEvent(t, e, "b", "two", nil)
	logEvent(t, e, "c", "three", nil)
	rows, cursor, err := httpapi.NextEvents(e.d, a, 200)
	testutil.Must(t, err)
	latest, _ := httpapi.LatestEventID(e.d)
	if len(rows) != 2 || rows[0].Kind != "b" || rows[1].Kind != "c" || cursor != latest {
		t.Fatalf("%v %d", rows, cursor)
	}
	rows, again, _ := httpapi.NextEvents(e.d, cursor, 200)
	if len(rows) != 0 || again != cursor {
		t.Fatal("not empty")
	}
}

func TestStatusSummaryIgnoresDraftsAndChosenPauses(t *testing.T) {
	e := newEnv(t, nil)
	tg := connectors.RowManifest{ID: "telegram", Kind: "channel", Name: "Telegram"}
	yes, no := true, false
	_, err := connectors.Upsert(e.d, connectors.UpsertInput{ID: "draft", Manifest: tg}, testutil.TestKey, 1)
	testutil.Must(t, err)
	_, err = connectors.Upsert(e.d, connectors.UpsertInput{ID: "telegram", Manifest: tg, Enabled: &yes}, testutil.TestKey, 1)
	testutil.Must(t, err)
	_, err = connectors.Upsert(e.d, connectors.UpsertInput{ID: "cliproxy", Manifest: connectors.RowManifest{ID: "cliproxy", Kind: "model", Name: "CLIProxyAPI"}, Enabled: &no}, testutil.TestKey, 1)
	testutil.Must(t, err)
	testutil.Must(t, connectors.SetStatus(e.d, "telegram", "on", nil, 1))
	testutil.Must(t, connectors.SetStatus(e.d, "cliproxy", "paused", nil, 1))
	sum, _ := httpapi.GetStatusSummary(e.d)
	if sum.Total != 2 || sum.On != 1 || len(sum.Attention) != 0 {
		t.Fatalf("%+v", sum)
	}
	detail := "401 from Telegram"
	testutil.Must(t, connectors.SetStatus(e.d, "telegram", "error", &detail, 2))
	sum, _ = httpapi.GetStatusSummary(e.d)
	if len(sum.Attention) != 1 || sum.Attention[0] != (httpapi.Attention{ID: "telegram", Label: "Telegram", Status: "error"}) {
		t.Fatalf("%+v", sum)
	}
}

func TestEventsStreamSendsASummaryFirstThenNewRowsAsTheyAreWritten(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	s := openSSE(t, e, "/api/events/stream", cookie(token))
	if ct := s.res.Header.Get("content-type"); s.res.StatusCode != 200 || ct != "text/event-stream; charset=utf-8" || s.res.Header.Get("x-accel-buffering") != "no" {
		t.Fatalf("%d %s", s.res.StatusCode, ct)
	}
	if f := s.next(); !strings.HasPrefix(f, "event: summary\ndata: {\"total\":0") {
		t.Fatalf("%q", f)
	}
	// Pushed in process: no poll interval to wait for.
	tg := "telegram"
	id := logEvent(t, e, "status", "on", &tg)
	f := s.nextData()
	if !strings.HasPrefix(f, "id: "+itoa(id)+"\nevent: events\ndata: [{\"id\":"+itoa(id)) {
		t.Fatalf("%q", f)
	}
	if f := s.nextData(); !strings.HasPrefix(f, "event: summary") {
		t.Fatalf("%q", f)
	}
	// A row without a connector: no summary after it.
	id2 := logEvent(t, e, "note", "plain", nil)
	if f := s.nextData(); !strings.HasPrefix(f, "id: "+itoa(id2)+"\nevent: events") {
		t.Fatalf("%q", f)
	}
	// Quiet: a keepalive comment.
	if f := s.next(); f != ": keepalive\n\n" {
		t.Fatalf("%q", f)
	}
}

func TestEventsStreamCatchesUpFromLastEventID(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	first := logEvent(t, e, "a", "one", nil)
	logEvent(t, e, "b", "two", nil)
	logEvent(t, e, "c", "three", nil)
	s := openSSE(t, e, "/api/events/stream", cookie(token), header("last-event-id", itoa(first)))
	s.next()
	f := s.nextData()
	if !strings.Contains(f, `"kind":"b"`) || !strings.Contains(f, `"kind":"c"`) || strings.Contains(f, `"kind":"a"`) {
		t.Fatalf("%q", f)
	}
	// ?after= works the same; without either the stream starts at the latest row.
	s2 := openSSE(t, e, "/api/events/stream?after=0", cookie(token))
	s2.next()
	if f := s2.nextData(); !strings.Contains(f, `"kind":"a"`) {
		t.Fatalf("%q", f)
	}
}

func TestStreamsNeedASession(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/api/events/stream", "/api/chat/stream"} {
		if res := e.do("GET", p, ""); res.StatusCode != 401 || readBody(res) != `{"error":"UNAUTHORIZED"}` {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
	}
}

// The recovery invite deletes every session: open streams send the
// unauthorized frame and close within one check.
func TestStreamStopsOnceTheSessionIsDeleted(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	s := openSSE(t, e, "/api/events/stream", cookie(token))
	if f := s.next(); !strings.HasPrefix(f, "event: summary") {
		t.Fatalf("%q", f)
	}
	logEvent(t, e, "a", "before", nil)
	if f := s.nextData(); !strings.Contains(f, "before") {
		t.Fatalf("%q", f)
	}
	_, err := e.d.Exec("DELETE FROM session")
	testutil.Must(t, err)
	var rest []string
	for {
		f := s.next()
		if f == "" {
			break
		}
		rest = append(rest, f)
	}
	if len(rest) == 0 || rest[len(rest)-1] != httpapi.UnauthorizedFrame {
		t.Fatalf("%q", rest)
	}
	logEvent(t, e, "b", "after", nil)
	if strings.Contains(strings.Join(rest, ""), "after") {
		t.Fatal("sent after revoke")
	}
}

func TestStreamsCloseOnShutdownWithoutTheUnauthorizedFrame(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	s := openSSE(t, e, "/api/events/stream", cookie(token))
	s.next()
	go e.s.CloseStreams()
	for {
		f := s.next()
		if f == "" {
			break
		}
		if f == httpapi.UnauthorizedFrame {
			t.Fatal("unauthorized frame on shutdown")
		}
	}
}

func TestChatStreamSendsTurnEventsAsDataFrames(t *testing.T) {
	bus := chat.NewBus()
	e := newEnv(t, func(o *httpapi.Options) { o.Runtime = &httpapi.Runtime{DB: o.DB, Bus: bus} })
	token := e.signIn()
	s := openSSE(t, e, "/api/chat/stream", cookie(token))
	if f := s.next(); f != ": connected\n\n" {
		t.Fatalf("%q", f)
	}
	for bus.Size() == 0 {
		time.Sleep(time.Millisecond)
	}
	bus.Publish(chat.TurnEvent{Type: "turn-start", ChatID: "c1", RunID: "r1"})
	if f := s.nextData(); f != "data: {\"type\":\"turn-start\",\"chatId\":\"c1\",\"runId\":\"r1\"}\n\n" {
		t.Fatalf("%q", f)
	}
	// A revoked session ends the chat stream too.
	testutil.Must(t, auth.DeleteSession(e.d, token))
	for {
		f := s.next()
		if f == "" || f == httpapi.UnauthorizedFrame {
			break
		}
	}
}

func TestChatStreamIs503WithoutTheRuntime(t *testing.T) {
	e := newEnv(t, nil)
	token := e.signIn()
	res := e.do("GET", "/api/chat/stream", "", cookie(token))
	if res.StatusCode != 503 || readBody(res) != `{"error":"`+httpapi.RuntimeDown+`"}` {
		t.Fatal(res.StatusCode)
	}
}
