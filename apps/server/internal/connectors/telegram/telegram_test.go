package telegram_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/config"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram/tgtest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/secret"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

const token = "123456789:AAbbccddeeffgghhiijjkkllmmnnooppqq"

const (
	ownerJSON    = `{"id":42,"is_bot":false,"first_name":"Zaki","last_name":"M"}`
	strangerJSON = `{"id":77,"is_bot":false,"first_name":"Orang"}`
)

var updateID = 1

// messageUpdate is messageUpdate(message, from): a private message from
// `from` (owner by default) with extra fields merged in.
func messageUpdate(message string, from string) string {
	if from == "" {
		from = ownerJSON
	}
	var f map[string]any
	_ = json.Unmarshal([]byte(from), &f)
	base := map[string]any{"message_id": float64(updateID + 501), "date": 1_700_000_000, "chat": map[string]any{"id": f["id"], "type": "private", "first_name": f["first_name"]}, "from": f}
	extra := map[string]any{}
	if message != "" {
		_ = json.Unmarshal([]byte(message), &extra)
	}
	for k, v := range extra {
		base[k] = v
	}
	b, _ := json.Marshal(map[string]any{"update_id": updateID, "message": base})
	updateID++
	return string(b)
}

type env struct {
	fake    *tgtest.Fake
	rt      *connectors.Runtime
	ctx     *conntest.ChannelCtx
	clock   *testutil.FakeClock
	fetched []string
	fetch   func(url string) ([]byte, error)
}

func useConfig(t *testing.T, c config.Config) {
	config.Set(&c)
	t.Cleanup(func() { config.Set(nil) })
}

func setup(t *testing.T) *env {
	if t != nil {
		useConfig(t, config.Config{Mode: "prod"})
	}
	e := &env{fake: tgtest.New(), clock: testutil.NewFakeClock(time.Now().UnixMilli())}
	e.rt = telegram.New(telegram.Options{
		NewTransport: e.fake.NewTransport,
		FetchFile: func(_ context.Context, url string) ([]byte, error) {
			e.fetched = append(e.fetched, url)
			if e.fetch != nil {
				return e.fetch(url)
			}
			parts := strings.Split(url, "/")
			return []byte("bytes:" + parts[len(parts)-1]), nil
		},
		Timers: e.clock,
		Now:    e.clock.Now,
		Sleep:  func(context.Context, int64) error { return nil },
	}, telegram.Manifest)
	e.ctx = conntest.NewChannelCtx(t, "telegram", "telegram", nil, map[string]string{"botToken": token}, "")
	return e
}

func (e *env) start(t *testing.T) *connectors.RunningConnector {
	t.Helper()
	r, err := e.rt.Start(e.ctx.Context)
	testutil.Must(t, err)
	t.Cleanup(func() { _ = r.Stop(context.Background()) })
	e.fake.WaitPolling()
	return r
}

func (e *env) claimed(t *testing.T) *connectors.RunningConnector {
	r := e.start(t)
	c, err := owner.CreateClaim(e.ctx.DB, "telegram", time.Now().UnixMilli())
	testutil.Must(t, err)
	e.fake.FeedJSON(messageUpdate(`{"text":"/start `+c.Nonce+`"}`, ""))
	return r
}

func lastPayload(t *testing.T, f *tgtest.Fake, method string) *jsonx.Object {
	t.Helper()
	calls := f.CallsTo(method)
	if len(calls) == 0 {
		t.Fatalf("no %s calls", method)
	}
	return calls[len(calls)-1]
}

func get(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

func pj(s string) any {
	v, err := jsonx.Parse([]byte(s))
	if err != nil {
		panic(err)
	}
	return v
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	if jsonx.Stringify(got) != jsonx.Stringify(want) {
		t.Fatalf("got  %s\nwant %s", jsonx.Stringify(got), jsonx.Stringify(want))
	}
}

func TestManifest(t *testing.T) {
	sec := jsonx.NewMap[string]()
	sec.Set("botToken", token)
	testutil.Must(t, telegram.Manifest.ParseSecrets(sec))
	sec.Set("botToken", "nope")
	if telegram.Manifest.ParseSecrets(sec) == nil {
		t.Fatal("nope accepted")
	}
	var steps []string
	for _, s := range telegram.Manifest.Setup {
		if s.Field != "" {
			steps = append(steps, s.Field)
		} else {
			steps = append(steps, s.Action)
		}
	}
	eq(t, steps, []string{"botToken", "claimOwner"})
}

func TestTest(t *testing.T) {
	t.Run("reports the bot username", func(t *testing.T) {
		e := setup(t)
		r, _ := e.rt.Test(e.ctx.Context)
		eq(t, r, connectors.TestResult{OK: true, Detail: "@palui_test_bot reachable", Data: pj(`{"username":"palui_test_bot"}`).(*jsonx.Object)})
	})
	t.Run("explains a rejected token", func(t *testing.T) {
		e := setup(t)
		e.fake.On("getMe", func(*jsonx.Object) any { return &tgtest.APIError{Code: 401, Description: "Unauthorized"} })
		r, _ := e.rt.Test(e.ctx.Context)
		eq(t, r, connectors.TestResult{OK: false, Detail: telegram.ErrUnauthorized})
	})
	t.Run("explains a missing token", func(t *testing.T) {
		e := setup(t)
		e.ctx.Secrets = jsonx.NewMap[string]()
		r, _ := e.rt.Test(e.ctx.Context)
		eq(t, r, connectors.TestResult{OK: false, Detail: telegram.ErrMissingToken})
	})
}

func TestStart(t *testing.T) {
	t.Run("polls, reports on, learns the username and stops cleanly", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		conntest.Until(t, func() bool { return e.ctx.LastStatus() == "on" })
		var statuses []string
		for _, s := range e.ctx.StatusList() {
			statuses = append(statuses, s.Status)
		}
		eq(t, statuses, []string{"connecting", "on"})
		eq(t, e.ctx.PatchList(), []any{pj(`{"botUsername":"palui_test_bot"}`)})
		eq(t, r.Channel.Reachable(), true)
		eq(t, r.Live(), pj(`{"username":"palui_test_bot","ownerClaimed":false,"polling":true}`))
		testutil.Must(t, r.Stop(context.Background()))
		eq(t, r.Channel.Reachable(), false)
		eq(t, e.ctx.LastStatus(), "on")
		// The last update is confirmed on stop.
		eq(t, len(e.fake.CallsTo("getUpdates")), 0)
	})
	t.Run("maps a 409 conflict to a clear error", func(t *testing.T) {
		e := setup(t)
		e.fake.On("getUpdates", func(*jsonx.Object) any {
			return &tgtest.APIError{Code: 409, Description: "Conflict: terminated by other getUpdates request"}
		})
		r, err := e.rt.Start(e.ctx.Context)
		testutil.Must(t, err)
		conntest.Until(t, func() bool { return e.ctx.LastStatus() == "error" })
		last := e.ctx.LastStatusFull()
		eq(t, *last.Detail, telegram.ErrConflict)
		if !strings.Contains(telegram.ErrConflict, "409 Conflict") || !strings.Contains(telegram.ErrConflict, "@BotFather") {
			t.Fatal(telegram.ErrConflict)
		}
		eq(t, r.Channel.Reachable(), false)
		testutil.Must(t, r.Stop(context.Background()))
	})
	t.Run("maps a 401 at getMe to the token error", func(t *testing.T) {
		e := setup(t)
		e.fake.On("getMe", func(*jsonx.Object) any { return &tgtest.APIError{Code: 401, Description: "Unauthorized"} })
		r, err := e.rt.Start(e.ctx.Context)
		testutil.Must(t, err)
		conntest.Until(t, func() bool { return e.ctx.LastStatus() == "error" })
		eq(t, *e.ctx.LastStatusFull().Detail, telegram.ErrUnauthorized)
		testutil.Must(t, r.Stop(context.Background()))
	})
	t.Run("refuses a prod token in dev mode", func(t *testing.T) {
		e := setup(t)
		useConfig(t, config.Config{Mode: "dev", ProdTokenHashes: []string{strings.ToUpper(secret.Sha256(token))}})
		_, err := e.rt.Start(e.ctx.Context)
		if err == nil || err.Error() != telegram.ErrDevGuard {
			t.Fatal(err)
		}
		eq(t, len(e.ctx.StatusList()), 1)
		eq(t, *e.ctx.LastStatusFull().Detail, telegram.ErrDevGuard)
		eq(t, e.fake.Clients(), 0)
		r, _ := e.rt.Test(e.ctx.Context)
		eq(t, r.OK, false)
	})
	t.Run("allows a dev token in dev mode", func(t *testing.T) {
		e := setup(t)
		useConfig(t, config.Config{Mode: "dev", ProdTokenHashes: []string{secret.Sha256("other-token")}})
		e.start(t)
		conntest.Until(t, func() bool { return e.ctx.LastStatus() == "on" })
	})
	t.Run("stops when the connector context is cancelled", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		e.ctx.Abort()
		conntest.Until(t, func() bool { return !r.Channel.Reachable() })
	})
}

func TestOwnerClaim(t *testing.T) {
	t.Run("claims with a fresh nonce, then refuses reuse", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		link, err := r.Action(context.Background(), "claimLink", nil)
		testutil.Must(t, err)
		url := get(link, "url").(string)
		if !regexp.MustCompile(`^https://t\.me/palui_test_bot\?start=[\w-]+$`).MatchString(url) {
			t.Fatal(url)
		}
		if !strings.Contains(get(link, "qrSvg").(string), "<svg") {
			t.Fatal("no svg")
		}
		if _, ok := get(link, "expiresAt").(int64); !ok {
			t.Fatalf("expiresAt %T", get(link, "expiresAt"))
		}
		nonce := strings.Split(url, "start=")[1]

		e.fake.FeedJSON(messageUpdate(`{"text":"/start `+nonce+`","entities":[{"type":"bot_command","offset":0,"length":6}]}`, ""))
		last := lastPayload(t, e.fake, "sendMessage")
		eq(t, []any{get(last, "chat_id"), get(last, "text")}, []any{42, telegram.CopyClaimed})
		o, ok, _ := owner.GetOwner(e.ctx.DB, "telegram")
		eq(t, ok && o.ExternalID == "42", true)
		eq(t, get(r.Live(), "ownerClaimed"), true)

		e.fake.FeedJSON(messageUpdate(`{"text":"/start `+nonce+`"}`, strangerJSON))
		last = lastPayload(t, e.fake, "sendMessage")
		eq(t, []any{get(last, "chat_id"), get(last, "text")}, []any{77, telegram.CopyClaimFailed})
		o, _, _ = owner.GetOwner(e.ctx.DB, "telegram")
		eq(t, o.ExternalID, "42")
		eq(t, e.ctx.InboundCount(), 0)
	})
	t.Run("refuses an expired nonce", func(t *testing.T) {
		e := setup(t)
		e.start(t)
		c, err := owner.CreateClaim(e.ctx.DB, "telegram", time.Now().UnixMilli()-11*60_000)
		testutil.Must(t, err)
		e.fake.FeedJSON(messageUpdate(`{"text":"/start `+c.Nonce+`"}`, ""))
		eq(t, get(lastPayload(t, e.fake, "sendMessage"), "text"), telegram.CopyClaimFailed)
		_, ok, _ := owner.GetOwner(e.ctx.DB, "telegram")
		eq(t, ok, false)
		eq(t, e.ctx.InboundCount(), 0)
	})
	t.Run("claimLink learns the username first and refuses unknown actions", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		if _, err := r.Action(context.Background(), "nope", nil); err == nil || err.Error() != "Unknown Telegram action: nope" {
			t.Fatal(err)
		}
	})
}

func inboundJSON(m connectors.InboundMessage) string {
	b, _ := json.Marshal(m)
	v, _ := jsonx.Parse(b)
	return jsonx.Canonical(v)
}

func TestInbound(t *testing.T) {
	t.Run("forwards text with the reply target", func(t *testing.T) {
		e := setup(t)
		e.claimed(t)
		e.fake.FeedJSON(messageUpdate(`{"message_id":900,"text":"ingetin aku besok","reply_to_message":{"message_id":812,"date":0,"chat":{"id":42,"type":"private"}}}`, ""))
		in := e.ctx.Inbound()
		eq(t, inboundJSON(in[0]), inboundJSON(connectors.InboundMessage{
			Channel: "telegram", ConnectorID: "telegram", ChatExternalID: "42", ChatKind: "dm", ChatTitle: "Zaki M",
			SenderExternalID: "42", SenderName: "Zaki M", ChannelMsgID: "900", ReplyToChannelMsgID: "812", Text: "ingetin aku besok", At: 1_700_000_000_000,
		}))
	})
	t.Run("marks group messages that mention, command or reply to the bot as addressed", func(t *testing.T) {
		e := setup(t)
		e.claimed(t)
		group := `"chat":{"id":-100777,"type":"group","title":"Kebun"}`
		for _, m := range []string{
			`{` + group + `,"text":"ngobrol biasa"}`,
			`{` + group + `,"text":"@palui_test_bot halo","entities":[{"type":"mention","offset":0,"length":15}]}`,
			`{` + group + `,"text":"@other_bot halo","entities":[{"type":"mention","offset":0,"length":10}]}`,
			`{` + group + `,"text":"lanjut","reply_to_message":{"message_id":5,"date":0,` + group + `,"from":{"id":123456789,"is_bot":true,"first_name":"Palui"}}}`,
			`{` + group + `,"text":"/new@palui_test_bot","entities":[{"type":"bot_command","offset":0,"length":19}]}`,
		} {
			e.fake.FeedJSON(messageUpdate(m, strangerJSON))
		}
		var addressed []bool
		in := e.ctx.Inbound()
		for _, m := range in {
			addressed = append(addressed, m.Addressed)
		}
		eq(t, addressed, []bool{false, true, false, true, true})
		eq(t, []string{in[1].ChatKind, in[1].ChatTitle, in[1].ChatExternalID, in[1].SenderExternalID}, []string{"group", "Kebun", "-100777", "77"})
	})
	t.Run("downloads the largest photo as base64 jpeg with its caption", func(t *testing.T) {
		e := setup(t)
		e.claimed(t)
		e.fake.FeedJSON(messageUpdate(`{"caption":"ini apa?","photo":[{"file_id":"small","file_unique_id":"s","width":90,"height":90},{"file_id":"big","file_unique_id":"b","width":1280,"height":960},{"file_id":"mid","file_unique_id":"m","width":320,"height":240}]}`, ""))
		eq(t, e.fetched, []string{"https://api.telegram.org/file/bot" + token + "/files/big"})
		in := e.ctx.Inbound()[0]
		eq(t, in.Text, "ini apa?")
		eq(t, in.Media, []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg", Data: base64.StdEncoding.EncodeToString([]byte("bytes:big"))}})
	})
	t.Run("downloads voice notes with their duration", func(t *testing.T) {
		e := setup(t)
		e.claimed(t)
		e.fake.FeedJSON(messageUpdate(`{"voice":{"file_id":"v1","file_unique_id":"v","duration":7,"mime_type":"audio/ogg"}}`, ""))
		in := e.ctx.Inbound()[0]
		eq(t, in.Text, "")
		eq(t, in.Media, []connectors.InboundMedia{{Kind: "audio", MimeType: "audio/ogg", DurationSec: testutil.Ptr(7.0), Data: base64.StdEncoding.EncodeToString([]byte("bytes:v1"))}})
	})
	t.Run("reads small documents and skips big ones", func(t *testing.T) {
		e := setup(t)
		e.claimed(t)
		e.fake.FeedJSON(messageUpdate(`{"document":{"file_id":"d1","file_unique_id":"d","file_name":"catatan.txt","mime_type":"text/plain","file_size":10}}`, ""))
		eq(t, e.ctx.Inbound()[0].Media, []connectors.InboundMedia{{Kind: "document", MimeType: "text/plain", Filename: "catatan.txt", Data: base64.StdEncoding.EncodeToString([]byte("bytes:d1"))}})
		e.fake.FeedJSON(messageUpdate(`{"caption":"baca ini","document":{"file_id":"d2","file_unique_id":"e","file_name":"big.pdf","file_size":5242880}}`, ""))
		in := e.ctx.Inbound()[1]
		eq(t, len(in.Media), 0)
		if !strings.Contains(in.Text, "baca ini") || !strings.Contains(in.Text, "over the 1 MB limit") {
			t.Fatal(in.Text)
		}
		eq(t, len(e.fetched), 1)
	})
	t.Run("does not download media from strangers", func(t *testing.T) {
		e := setup(t)
		e.claimed(t)
		e.fake.FeedJSON(messageUpdate(`{"caption":"spam","photo":[{"file_id":"x","file_unique_id":"x","width":1,"height":1}]}`, strangerJSON))
		eq(t, len(e.fetched), 0)
		in := e.ctx.Inbound()[0]
		eq(t, []string{in.SenderExternalID, in.Text}, []string{"77", "spam"})
		eq(t, in.Media, []connectors.InboundMedia{{Kind: "image", MimeType: "image/jpeg"}})
	})
	// A guest's photo in a chat set up for guests never reached the model
	// (rpt_0mwkrtff0z3w0ld5vfp).
	t.Run("downloads a guest's media when the message will be answered", func(t *testing.T) {
		e := setup(t)
		e.claimed(t)
		e.ctx.Services.WantsMedia = func(m connectors.InboundMessage) bool { return m.SenderExternalID == "77" }
		e.fake.FeedJSON(messageUpdate(`{"photo":[{"file_id":"g1","file_unique_id":"g","width":1,"height":1}]}`, strangerJSON))
		in := e.ctx.Inbound()[0]
		eq(t, []any{in.Text, len(in.Media), len(e.fetched)}, []any{"", 1, 1})
		eq(t, in.Media[0].Data != "", true)
	})
	t.Run("a failed download is logged and the text still goes", func(t *testing.T) {
		e := setup(t)
		e.fetch = func(string) ([]byte, error) { return nil, errors.New("file download failed: HTTP 404") }
		e.claimed(t)
		e.fake.FeedJSON(messageUpdate(`{"caption":"lihat","photo":[{"file_id":"x","file_unique_id":"x","width":1,"height":1}]}`, ""))
		in := e.ctx.Inbound()[0]
		eq(t, in.Text, "lihat\n[image could not be downloaded]")
		eq(t, len(in.Media), 0)
	})
	t.Run("turns button presses into callbacks and answers them", func(t *testing.T) {
		e := setup(t)
		e.claimed(t)
		e.fake.FeedJSON(`{"update_id":900,"callback_query":{"id":"cb-1","from":` + ownerJSON + `,"chat_instance":"ci","data":"rem:done:r1","message":{"message_id":300,"date":0,"chat":{"id":42,"type":"private","first_name":"Zaki"}}}}`)
		in := e.ctx.Inbound()[0]
		eq(t, []any{in.ChatExternalID, in.SenderExternalID, in.Text, in.Callback, in.ReplyToChannelMsgID}, []any{"42", "42", "", &connectors.InboundCallback{Data: "rem:done:r1", CallbackID: "cb-1"}, "300"})
		eq(t, e.fake.CallsTo("answerCallbackQuery"), []any{pj(`{"callback_query_id":"cb-1"}`)})
		eq(t, in.At, e.clock.Now())
	})
}

func TestSend(t *testing.T) {
	t.Run("sends markdown as HTML with the keyboard and reply target", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		res, err := r.Channel.Send(context.Background(), "42", db.OutboxPayload{
			Text: "**Pengingat**: minum <obat>", Format: "markdown", ReplyToChannelMsgID: "55",
			Actions: []db.OutboxAction{{ID: "rem:done:r1", Label: "Done"}, {ID: "rem:snooze:r1:1h", Label: "1 jam"}, {ID: strings.Repeat("x", 65), Label: "too long"}},
		})
		testutil.Must(t, err)
		sent := e.fake.CallsTo("sendMessage")
		eq(t, len(sent), 1)
		if got := jsonx.Stringify(sent[0]); got != `{"chat_id":"42","text":"<b>Pengingat</b>: minum &lt;obat&gt;","reply_parameters":{"message_id":55,"allow_sending_without_reply":true},"reply_markup":{"inline_keyboard":[[{"text":"Done","callback_data":"rem:done:r1"},{"text":"1 jam","callback_data":"rem:snooze:r1:1h"}]]},"parse_mode":"HTML"}` {
			t.Fatal(got)
		}
		eq(t, res.ChannelMsgID, "100")
	})
	t.Run("splits long replies, keyboard on the last chunk only", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		para := strings.Repeat("Kalimat yang lumayan panjang buat ngetes pemotongan. ", 40)
		res, err := r.Channel.Send(context.Background(), "42", db.OutboxPayload{Text: para + "\n\n" + para + "\n\n" + para, Actions: []db.OutboxAction{{ID: "approve:a1", Label: "Ya"}}, ReplyToChannelMsgID: "9"})
		testutil.Must(t, err)
		sent := e.fake.CallsTo("sendMessage")
		if len(sent) < 2 {
			t.Fatal(len(sent))
		}
		for i, c := range sent {
			if strx.Len16(get(c, "text").(string)) > 4096 {
				t.Fatal("chunk too long")
			}
			_, markup := c.Get("reply_markup")
			_, reply := c.Get("reply_parameters")
			eq(t, []bool{markup, reply}, []bool{i == len(sent)-1, i == 0})
		}
		eq(t, res.ChannelMsgID, jsonx.NumberString(float64(100+len(sent)-1)))
	})
	t.Run("falls back to plain text when Telegram rejects the HTML", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		e.fake.On("sendMessage", func(p *jsonx.Object) any {
			if _, html := p.Get("parse_mode"); html {
				return &tgtest.APIError{Code: 400, Description: "Bad Request: can't parse entities: unexpected end tag"}
			}
			return map[string]any{"message_id": 7, "date": 0, "chat": map[string]any{"id": 42, "type": "private"}, "text": get(p, "text")}
		})
		res, err := r.Channel.Send(context.Background(), "42", db.OutboxPayload{Text: "**a *b** c*"})
		testutil.Must(t, err)
		sent := e.fake.CallsTo("sendMessage")
		eq(t, len(sent), 2)
		eq(t, jsonx.Stringify(sent[1]), `{"chat_id":"42","text":"**a *b** c*"}`)
		eq(t, res.ChannelMsgID, "7")
	})
	t.Run("rethrows other send errors so the outbox retries", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		e.fake.On("sendMessage", func(*jsonx.Object) any {
			return &tgtest.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}
		})
		_, err := r.Channel.Send(context.Background(), "42", db.OutboxPayload{Text: "hai"})
		if err == nil || !regexp.MustCompile(`403|Forbidden`).MatchString(err.Error()) {
			t.Fatal(err)
		}
	})
	t.Run("renders chunks whose HTML fits", func(t *testing.T) {
		chunks := telegram.RenderChunks(strings.Repeat("&", 5000), 4096)
		var plain strings.Builder
		for _, c := range chunks {
			if strx.Len16(c.HTML) > 4096 {
				t.Fatal("html too long")
			}
			plain.WriteString(c.Plain)
		}
		eq(t, plain.String(), strings.Repeat("&", 5000))
	})
}

func TestTyping(t *testing.T) {
	t.Run("repeats every 4 s until switched off", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		typer := r.Channel.(connectors.Typer)
		testutil.Must(t, typer.Typing(context.Background(), "42", true, connectors.TypingOptions{}))
		conntest.Until(t, func() bool { return len(e.fake.CallsTo("sendChatAction")) == 1 })
		eq(t, e.fake.CallsTo("sendChatAction"), []any{pj(`{"chat_id":"42","action":"typing"}`)})
		e.clock.Advance(8_100, nil)
		conntest.Until(t, func() bool { return len(e.fake.CallsTo("sendChatAction")) == 3 })
		testutil.Must(t, typer.Typing(context.Background(), "42", false, connectors.TypingOptions{}))
		e.clock.Advance(20_000, nil)
		conntest.Settle()
		eq(t, len(e.fake.CallsTo("sendChatAction")), 3)
	})
	t.Run("stops on send and on stop()", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		typer := r.Channel.(connectors.Typer)
		testutil.Must(t, typer.Typing(context.Background(), "42", true, connectors.TypingOptions{}))
		_, err := r.Channel.Send(context.Background(), "42", db.OutboxPayload{Text: "selesai"})
		testutil.Must(t, err)
		e.clock.Advance(10_000, nil)
		conntest.Settle()
		eq(t, len(e.fake.CallsTo("sendChatAction")), 1)
		testutil.Must(t, typer.Typing(context.Background(), "43", true, connectors.TypingOptions{}))
		testutil.Must(t, r.Stop(context.Background()))
		e.clock.Advance(10_000, nil)
		conntest.Settle()
		eq(t, len(e.fake.CallsTo("sendChatAction")), 2)
	})
	t.Run("stops by itself after the cap", func(t *testing.T) {
		e := setup(t)
		r := e.start(t)
		testutil.Must(t, r.Channel.(connectors.Typer).Typing(context.Background(), "42", true, connectors.TypingOptions{}))
		e.clock.Advance(telegram.DefaultTypingMax, nil)
		conntest.Until(t, func() bool {
			return len(e.fake.CallsTo("sendChatAction")) == 1+telegram.DefaultTypingMax/telegram.DefaultTypingInterval
		})
		e.clock.Advance(60_000, nil)
		conntest.Settle()
		eq(t, len(e.fake.CallsTo("sendChatAction")), 1+telegram.DefaultTypingMax/telegram.DefaultTypingInterval)
		eq(t, e.clock.Pending(), 0)
	})
}
