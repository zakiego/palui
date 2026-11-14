package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/flows"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// hooks-route.test.ts.

var hookNow = time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC).UnixMilli()
var ts = strconv.FormatInt(hookNow/1000, 10)

type hookEnv struct {
	*env
	hook   hooks.View
	secret string
}

func newHookEnv(t *testing.T) *hookEnv {
	e := newEnv(t, nil)
	e.now.Store(hookNow)
	chat, err := chats.Ensure(e.d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, 1000)
	testutil.Must(t, err)
	created, err := hooks.Create(e.d, hooks.CreateInput{Name: "CI", Mode: "forward", ChatID: chat.ID}, testutil.TestKey, 2000)
	testutil.Must(t, err)
	return &hookEnv{env: e, hook: created.Hook, secret: created.Secret}
}

func (h *hookEnv) post(slug string, body io.Reader, headers map[string]string) *http.Response {
	r := httptest.NewRequest("POST", "/api/hooks/"+slug, body)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	return rec.Result()
}

func signed(secret, t string, body []byte) map[string]string {
	return map[string]string{hooks.TimestampHeader: t, hooks.SignatureHeader: hooks.Sign(secret, t, body)}
}

const hookBody = `{"event":"deploy","status":"ok"}`

func TestHookAcceptsASignedRequestOnce(t *testing.T) {
	h := newHookEnv(t)
	res := h.post(h.hook.Slug, strings.NewReader(hookBody), signed(h.secret, ts, []byte(hookBody)))
	raw := readBody(res)
	var m map[string]any
	testutil.Must(t, json.Unmarshal([]byte(raw), &m))
	if res.StatusCode != 202 || !strings.HasPrefix(raw, `{"ok":true,"id":"msg_`) {
		t.Fatalf("%d %s", res.StatusCode, raw)
	}
	var text string
	testutil.Must(t, h.d.QueryRow("SELECT text FROM messages WHERE id = ?", m["id"]).Scan(&text))
	if !strings.Contains(text, "event: deploy") {
		t.Fatal(text)
	}
	// The same request again is a replay.
	if res := h.post(h.hook.Slug, strings.NewReader(hookBody), signed(h.secret, ts, []byte(hookBody))); res.StatusCode != 409 {
		t.Fatal(res.StatusCode)
	}
}

func TestHookRefusesAMissingOrStaleTimestampAndABodyOnlySignature(t *testing.T) {
	h := newHookEnv(t)
	sig := hooks.Sign(h.secret, ts, []byte(hookBody))
	if res := h.post(h.hook.Slug, strings.NewReader(hookBody), map[string]string{hooks.SignatureHeader: sig}); res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	old := strconv.FormatInt(hookNow/1000-6*60, 10)
	res := h.post(h.hook.Slug, strings.NewReader(hookBody), signed(h.secret, old, []byte(hookBody)))
	if m := decode(t, res); res.StatusCode != 401 || !strings.Contains(m["error"].(string), "5 minutes") {
		t.Fatalf("%d %v", res.StatusCode, m)
	}
	moved := strconv.FormatInt(hookNow/1000+1, 10)
	if res := h.post(h.hook.Slug, strings.NewReader(hookBody), map[string]string{hooks.TimestampHeader: moved, hooks.SignatureHeader: sig}); res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
}

func TestHookVerifiesOverTheRawBytes(t *testing.T) {
	h := newHookEnv(t)
	raw := []byte{0x7b, 0x22, 0x61, 0x22, 0x3a, 0x22, 0xff, 0xfe, 0x22, 0x7d}
	if res := h.post(h.hook.Slug, bytes.NewReader(raw), signed(h.secret, ts, raw)); res.StatusCode != 202 {
		t.Fatal(res.StatusCode)
	}
}

func TestHookRefusesAnOversizedContentLengthBeforeLookingUpTheSlug(t *testing.T) {
	h := newHookEnv(t)
	res := h.post("nope", strings.NewReader("x"), map[string]string{"content-length": strconv.Itoa(300 * 1024)})
	if res.StatusCode != 413 {
		t.Fatal(res.StatusCode)
	}
	if body := readBody(res); body != `{"ok":false,"error":"Body is too large"}` {
		t.Fatal(body)
	}
}

type pullReader struct{ pulled bool }

func (p *pullReader) Read(b []byte) (int, error) {
	p.pulled = true
	return 0, io.EOF
}

func TestHookLooksUpTheSlugBeforeReadingTheBody(t *testing.T) {
	h := newHookEnv(t)
	body := &pullReader{}
	if res := h.post("nope", body, nil); res.StatusCode != 404 || body.pulled {
		t.Fatal(res.StatusCode, body.pulled)
	}
}

type endless struct{ sent int }

func (e *endless) Read(b []byte) (int, error) {
	e.sent += len(b)
	return len(b), nil
}

func TestHookCutsOffAStreamedBodyOver256KB(t *testing.T) {
	h := newHookEnv(t)
	body := &endless{}
	r := httptest.NewRequest("POST", "/api/hooks/"+h.hook.Slug, body)
	r.ContentLength = -1
	r.Header.Del("content-length")
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	if rec.Code != 413 || body.sent > 10*64*1024 {
		t.Fatal(rec.Code, body.sent)
	}
}

func TestHookInFlowModeStartsTheListeningFlowOncePerDelivery(t *testing.T) {
	h := newHookEnv(t)
	def := jsonx.NewMap[any]()
	if err := json.Unmarshal([]byte(`{"name":"PR","triggers":[{"id":"gh","kind":"webhook","hookId":"`+h.hook.ID+`","start":"say"}],"steps":[{"id":"say","kind":"message","text":"PR {{trigger.payload.number}}"}]}`), def); err != nil {
		t.Fatal(err)
	}
	saved, err := flows.CreateFlow(h.d, flows.SaveInput{ChatID: h.hook.ChatID, CreatedBy: "test", Definition: def}, hookNow)
	testutil.Must(t, err)
	var mode string
	testutil.Must(t, h.d.QueryRow("SELECT mode FROM hooks WHERE id = ?", h.hook.ID).Scan(&mode))
	if mode != "flow" {
		t.Fatal(mode)
	}
	payload := []byte(`{"number":46}`)
	send := func(t string) *http.Response {
		hd := signed(h.secret, t, payload)
		hd["x-github-delivery"] = "d-123"
		hd["x-github-event"] = "pull_request"
		return h.post(h.hook.Slug, bytes.NewReader(payload), hd)
	}
	res := send(ts)
	if body := readBody(res); res.StatusCode != 202 || body != `{"ok":true,"runs":1}` {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	// GitHub redelivers with a new signature but the same delivery id: no second run.
	if body := readBody(send(strconv.FormatInt(hookNow/1000+1, 10))); body != `{"ok":true,"runs":0}` {
		t.Fatal(body)
	}
	var n int
	var runKey, taint, trigger, status string
	testutil.Must(t, h.d.QueryRow("SELECT count(*) FROM flow_runs WHERE flow_id = ?", saved.Flow.ID).Scan(&n))
	testutil.Must(t, h.d.QueryRow("SELECT run_key, taint, trigger, status FROM flow_runs WHERE flow_id = ?", saved.Flow.ID).Scan(&runKey, &taint, &trigger, &status))
	if n != 1 || runKey != "hook:d-123" || taint != `["trigger"]` || status != "queued" || !strings.Contains(trigger, `"payload":{"number":46}`) || !strings.Contains(trigger, `"x-github-event":"pull_request"`) {
		t.Fatalf("%d %s %s %s %s", n, runKey, taint, trigger, status)
	}
	testutil.Must(t, h.d.QueryRow("SELECT count(*) FROM messages").Scan(&n))
	if n != 0 {
		t.Fatal("a message was written")
	}
}
