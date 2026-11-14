package hooks_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/chats"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/hooks"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

// Ports packages/core/src/hooks/index.test.ts.

const KEY = "test-master-key-0123456789-abcdefghijklmnop"

type fixture struct {
	d      *db.DB
	chat   db.Chat
	hook   hooks.View
	secret string
}

func setup(t *testing.T) fixture {
	t.Helper()
	d := testutil.DB(t)
	chat, err := chats.Ensure(d, chats.EnsureInput{Channel: "telegram", ExternalID: "42"}, 1000)
	testutil.Must(t, err)
	c, err := hooks.Create(d, hooks.CreateInput{Name: "GitHub", Mode: "forward", ChatID: chat.ID}, KEY, 2000)
	testutil.Must(t, err)
	return fixture{d, chat, c.Hook, c.Secret}
}

var (
	body = `{"action":"opened","number":7}`
	NOW  = time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC).UnixMilli()
	TS   = strconv.FormatInt(NOW/1000, 10)
)

func signed(secret, raw, ts string) hooks.Headers {
	sig := hooks.Sign(secret, ts, []byte(raw))
	return hooks.Headers{Timestamp: &ts, Signature: &sig}
}

func verify(t *testing.T, f fixture, slug, raw string, h hooks.Headers, now int64) *hooks.Failure {
	t.Helper()
	_, fail, err := hooks.VerifyRequest(f.d, slug, []byte(raw), h, KEY, now)
	testutil.Must(t, err)
	return fail
}

func status(fail *hooks.Failure) int {
	if fail == nil {
		return 0
	}
	return fail.Status
}

func TestCreateReturnsThePlaintextSecretOnceAndStoresItEncrypted(t *testing.T) {
	f := setup(t)
	if !strings.HasPrefix(f.secret, "phk_") || !regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`).MatchString(f.hook.Slug) {
		t.Fatalf("%+v %s", f.hook, f.secret)
	}
	if strings.Contains(jsonx.Stringify(f.hook), "secret") {
		t.Fatal("view has the secret")
	}
	var stored string
	testutil.Must(t, f.d.QueryRow("SELECT secret FROM hooks WHERE id = ?", f.hook.ID).Scan(&stored))
	if strings.Contains(stored, f.secret) || !strings.HasPrefix(stored, "v1.") {
		t.Fatal(stored)
	}
	list, _ := hooks.List(f.d)
	if len(list) != 1 || list[0].ID != f.hook.ID {
		t.Fatalf("%+v", list)
	}
}

func TestCreateRefusesAnUnknownChat(t *testing.T) {
	d := testutil.DB(t)
	if _, err := hooks.Create(d, hooks.CreateInput{Name: "x", Mode: "forward", ChatID: "chat_nope"}, KEY, 1); err == nil || !strings.Contains(err.Error(), "No chat") {
		t.Fatal(err)
	}
}

func TestAcceptsAValidSignatureOverTimestampAndRawBody(t *testing.T) {
	f := setup(t)
	mac := hmac.New(sha256.New, []byte(f.secret))
	mac.Write([]byte(TS + "." + body))
	if hooks.Sign(f.secret, TS, []byte(body)) != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("signature format")
	}
	if fail := verify(t, f, f.hook.Slug, body, signed(f.secret, body, TS), NOW); fail != nil {
		t.Fatalf("%+v", fail)
	}
}

func TestRejectsMissingWrongAndRotatedSignatures(t *testing.T) {
	f := setup(t)
	ts := TS
	if status(verify(t, f, f.hook.Slug, body, hooks.Headers{Timestamp: &ts}, NOW)) != 401 {
		t.Fatal("missing")
	}
	bad := "sha256=deadbeef"
	if status(verify(t, f, f.hook.Slug, body, hooks.Headers{Timestamp: &ts, Signature: &bad}, NOW)) != 401 {
		t.Fatal("wrong")
	}
	if status(verify(t, f, f.hook.Slug, body+" ", signed(f.secret, body, TS), NOW)) != 401 {
		t.Fatal("body changed")
	}
	mac := hmac.New(sha256.New, []byte(f.secret))
	mac.Write([]byte(body))
	legacy := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if status(verify(t, f, f.hook.Slug, body, hooks.Headers{Timestamp: &ts, Signature: &legacy}, NOW)) != 401 {
		t.Fatal("legacy accepted")
	}
	rotated, err := hooks.RotateSecret(f.d, f.hook.ID, KEY)
	testutil.Must(t, err)
	if rotated.Secret == f.secret {
		t.Fatal("same secret")
	}
	if status(verify(t, f, f.hook.Slug, body, signed(f.secret, body, TS), NOW)) != 401 {
		t.Fatal("old secret accepted")
	}
	if fail := verify(t, f, f.hook.Slug, body, signed(rotated.Secret, body, TS), NOW); fail != nil {
		t.Fatalf("%+v", fail)
	}
}

func TestRequiresATimestampWithinFiveMinutes(t *testing.T) {
	f := setup(t)
	sig := hooks.Sign(f.secret, TS, []byte(body))
	fail := verify(t, f, f.hook.Slug, body, hooks.Headers{Signature: &sig}, NOW)
	if status(fail) != 401 || !strings.Contains(fail.Error, "x-palui-timestamp") {
		t.Fatalf("%+v", fail)
	}
	if status(verify(t, f, f.hook.Slug, body, signed(f.secret, body, "soon"), NOW)) != 401 {
		t.Fatal("soon")
	}
	n, _ := strconv.ParseInt(TS, 10, 64)
	for _, off := range []int64{-301, 301} {
		fail := verify(t, f, f.hook.Slug, body, signed(f.secret, body, strconv.FormatInt(n+off, 10)), NOW)
		if status(fail) != 401 || !strings.Contains(fail.Error, "5 minutes") {
			t.Fatalf("%d: %+v", off, fail)
		}
	}
	if fail := verify(t, f, f.hook.Slug, body, signed(f.secret, body, strconv.FormatInt(n-299, 10)), NOW); fail != nil {
		t.Fatalf("%+v", fail)
	}
}

func TestRefusesAReplay(t *testing.T) {
	f := setup(t)
	h := signed(f.secret, body, TS)
	if fail := verify(t, f, f.hook.Slug, body, h, NOW); fail != nil {
		t.Fatalf("%+v", fail)
	}
	if status(verify(t, f, f.hook.Slug, body, h, NOW+1000)) != 409 {
		t.Fatal("replay accepted")
	}
}

func TestSignsAndVerifiesRawBytes(t *testing.T) {
	f := setup(t)
	raw := string([]byte{0x22, 0xff, 0x22})
	if fail := verify(t, f, f.hook.Slug, raw, signed(f.secret, raw, TS), NOW); fail != nil {
		t.Fatalf("%+v", fail)
	}
	if hooks.BodyText([]byte(raw)) != "\"\xef\xbf\xbd\"" {
		t.Fatal(hooks.BodyText([]byte(raw)))
	}
}

func TestNotFoundDisabledAndPaused(t *testing.T) {
	f := setup(t)
	if _, fail, _ := hooks.Lookup(f.d, "nope"); status(fail) != 404 {
		t.Fatal("lookup")
	}
	if status(verify(t, f, "nope", body, signed(f.secret, body, TS), NOW)) != 404 {
		t.Fatal("verify 404")
	}
	_, _, err := hooks.SetEnabled(f.d, f.hook.ID, false)
	testutil.Must(t, err)
	if status(verify(t, f, f.hook.Slug, body, signed(f.secret, body, TS), NOW)) != 503 {
		t.Fatal("disabled")
	}
	hooks.SetEnabled(f.d, f.hook.ID, true)
	m := connectors.RowManifest{ID: "hooks", Kind: "skill", Name: "Webhooks"}
	_, err = connectors.Upsert(f.d, connectors.UpsertInput{ID: "hooks", Manifest: m, Enabled: testutil.Ptr(false)}, KEY, 1)
	testutil.Must(t, err)
	fail := verify(t, f, f.hook.Slug, body, signed(f.secret, body, TS), NOW)
	if status(fail) != 503 || fail.Error != "Hooks are paused" {
		t.Fatalf("%+v", fail)
	}
	_, err = connectors.Upsert(f.d, connectors.UpsertInput{ID: "hooks", Manifest: m, Enabled: testutil.Ptr(true)}, KEY, 1)
	testutil.Must(t, err)
	if fail := verify(t, f, f.hook.Slug, body, signed(f.secret, body, TS), NOW); fail != nil {
		t.Fatalf("%+v", fail)
	}
}

func TestRejectsOversizedBodies(t *testing.T) {
	f := setup(t)
	big := strings.Repeat("x", 300*1024)
	if status(verify(t, f, f.hook.Slug, big, signed(f.secret, big, TS), NOW)) != 413 {
		t.Fatal("big")
	}
}

func TestIsGoneAfterDelete(t *testing.T) {
	f := setup(t)
	if ok, _ := hooks.Delete(f.d, f.hook.ID); !ok {
		t.Fatal("delete")
	}
	if status(verify(t, f, f.hook.Slug, body, signed(f.secret, body, TS), NOW)) != 404 {
		t.Fatal("still there")
	}
}

func TestReadBodyReadsBytesAndStopsPastTheCap(t *testing.T) {
	r := httptest.NewRequest("POST", "http://x/", strings.NewReader("hello"))
	b, fail := hooks.ReadBody(r, 10)
	if fail != nil || hooks.BodyText(b) != "hello" {
		t.Fatal(fail)
	}
	r = httptest.NewRequest("POST", "http://x/", strings.NewReader(strings.Repeat("x", 11)))
	r.Header.Del("content-length")
	if _, fail := hooks.ReadBody(r, 10); status(fail) != 413 {
		t.Fatal("cap")
	}
	r = httptest.NewRequest("POST", "http://x/", strings.NewReader("x"))
	r.Header.Set("content-length", "999")
	if _, fail := hooks.ReadBody(r, 10); status(fail) != 413 {
		t.Fatal("declared")
	}
}

func TestIngestInsertsAPendingUserMessageAndUpdatesLastAt(t *testing.T) {
	f := setup(t)
	raw := `{"repo":{"name":"palui"},"action":"opened","labels":["bug","p1"]}`
	now := time.Now().UnixMilli()
	ts := strconv.FormatInt(now/1000, 10)
	hook, fail, err := hooks.VerifyRequest(f.d, f.hook.Slug, []byte(raw), signed(f.secret, raw, ts), KEY, now)
	if err != nil || fail != nil {
		t.Fatal(fail, err)
	}
	msg, err := hooks.Ingest(f.d, hook, raw, 5000)
	testutil.Must(t, err)
	if msg.Status != "pending" || msg.Role != "user" || msg.ChatID != f.chat.ID || *msg.SessionID != *f.chat.SessionID || *msg.SenderID != "hook:"+f.hook.Slug {
		t.Fatalf("%+v", msg)
	}
	if string(msg.Content) != `{"text":`+jsonx.Stringify(msg.Text)+`,"hook":{"id":"`+f.hook.ID+`","name":"GitHub","mode":"forward"}}` {
		t.Fatal(string(msg.Content))
	}
	if msg.Text != "Webhook GitHub:\nrepo:\n  name: palui\naction: opened\nlabels: bug, p1" {
		t.Fatal(msg.Text)
	}
	list, _ := hooks.List(f.d)
	if *list[0].LastAt != 5000 {
		t.Fatal(list[0].LastAt)
	}
}

func TestSummarizePassesNonJSONThroughAndCapsTheLength(t *testing.T) {
	if got := hooks.SummarizePayload("Uptime", "  server down  "); got != "Webhook Uptime:\nserver down" {
		t.Fatal(got)
	}
	items := make([]any, 2000)
	for i := range items {
		o := jsonx.NewMap[any]()
		o.Set("i", float64(i))
		o.Set("note", "lorem ipsum")
		items[i] = o
	}
	payload := jsonx.NewMap[any]()
	payload.Set("items", items)
	long := hooks.SummarizePayload("Big", jsonx.Stringify(payload))
	if strx.Len16(long) != hooks.MaxTextChars || !strings.HasSuffix(long, "[truncated]") {
		t.Fatal(len(long))
	}
}

func TestSummarizeRendersArraysOfObjects(t *testing.T) {
	if got := hooks.SummarizePayload("x", `[{"a":1},2]`); got != "Webhook x:\n-\n  a: 1\n- 2" {
		t.Fatal(got)
	}
}
