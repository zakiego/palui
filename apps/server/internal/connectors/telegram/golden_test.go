package telegram_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors/telegram"
	"github.com/zakiego/palui/apps/server/internal/connectors/telegram/tgtest"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/owner"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type goldenFile struct {
	Render []struct {
		MD     string                   `json:"md"`
		Limit  int                      `json:"limit"`
		Chunks []telegram.RenderedChunk `json:"chunks"`
	} `json:"render"`
	TgSends []struct {
		Name    string           `json:"name"`
		Fail    *string          `json:"fail"`
		Payload db.OutboxPayload `json:"payload"`
		Calls   json.RawMessage  `json:"calls"`
		Result  struct {
			OK    *struct{ ChannelMsgID *string } `json:"ok"`
			Error *string                         `json:"error"`
		} `json:"result"`
	} `json:"tgSends"`
	TgInbound []struct {
		Claim   *int64          `json:"claim"`
		Name    string          `json:"name"`
		Update  json.RawMessage `json:"update"`
		Inbound []any           `json:"inbound"`
		Calls   json.RawMessage `json:"calls"`
		Fetched []string        `json:"fetched"`
	} `json:"tgInbound"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	raw, err := os.ReadFile("../../../testdata/channels.json")
	testutil.Must(t, err)
	var g goldenFile
	testutil.Must(t, json.Unmarshal(raw, &g))
	return g
}

func ordered(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	v, err := jsonx.Parse(raw)
	testutil.Must(t, err)
	return jsonx.Stringify(v)
}

// callsJSON is the recorded calls as the golden lists them.
func callsJSON(calls []tgtest.Call) string {
	out := []any{}
	for _, c := range calls {
		if c.Method == "getMe" || c.Method == "deleteWebhook" {
			continue
		}
		o := jsonx.NewMap[any]()
		o.Set("method", c.Method)
		o.Set("payload", c.Payload)
		out = append(out, o)
	}
	return jsonx.Stringify(out)
}

func TestGoldenRenderChunks(t *testing.T) {
	g := loadGolden(t)
	for i, c := range g.Render {
		got := telegram.RenderChunks(c.MD, c.Limit)
		if jsonx.Stringify(got) != jsonx.Stringify(c.Chunks) {
			t.Errorf("render %d: %d chunks, want %d", i, len(got), len(c.Chunks))
		}
	}
}

func TestGoldenSends(t *testing.T) {
	g := loadGolden(t)
	if len(g.TgSends) < 10 {
		t.Fatal("too few sends")
	}
	for _, s := range g.TgSends {
		t.Run(s.Name, func(t *testing.T) {
			e := setup(t)
			next := int64(100)
			ok := func(p *jsonx.Object) any {
				id := next
				next++
				return map[string]any{"message_id": id, "date": 0, "chat": map[string]any{"id": 42, "type": "private"}, "text": get(p, "text")}
			}
			fail := ""
			if s.Fail != nil {
				fail = *s.Fail
			}
			switch fail {
			case "parse":
				e.fake.On("sendMessage", func(p *jsonx.Object) any {
					if _, html := p.Get("parse_mode"); html {
						return &tgtest.APIError{Code: 400, Description: "Bad Request: can't parse entities: unexpected end tag"}
					}
					return ok(p)
				})
			case "parse-always":
				e.fake.On("sendMessage", func(p *jsonx.Object) any {
					if _, html := p.Get("parse_mode"); html {
						return &tgtest.APIError{Code: 400, Description: `Bad Request: unsupported start TAG "x"`}
					}
					return ok(p)
				})
			case "forbidden":
				e.fake.On("sendMessage", func(*jsonx.Object) any {
					return &tgtest.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}
				})
			}
			r := e.start(t)
			before := len(e.fake.Calls())
			res, err := r.Channel.Send(context.Background(), "42", s.Payload)
			if got, want := callsJSON(e.fake.Calls()[before:]), ordered(t, s.Calls); got != want {
				t.Fatalf("calls\n got  %s\n want %s\n %s", got, want, testutil.DiffAt(got, want))
			}
			switch {
			case s.Result.Error != nil:
				if err == nil || err.Error() != *s.Result.Error {
					t.Fatalf("error %v, want %q", err, *s.Result.Error)
				}
			case err != nil:
				t.Fatal(err)
			case s.Result.OK.ChannelMsgID == nil && res.ChannelMsgID != "":
				t.Fatalf("channelMsgId %q, want none", res.ChannelMsgID)
			case s.Result.OK.ChannelMsgID != nil && res.ChannelMsgID != *s.Result.OK.ChannelMsgID:
				t.Fatalf("channelMsgId %q, want %q", res.ChannelMsgID, *s.Result.OK.ChannelMsgID)
			}
		})
	}
}

// normInbound compares inbound messages by content: Go omits addressed:false
// (InboundMessage.Addressed is omitempty) where TS wrote it.
func normInbound(list []any) string {
	for _, m := range list {
		o := m.(map[string]any)
		if a, ok := o["addressed"]; ok && a == false {
			delete(o, "addressed")
		}
	}
	b, _ := json.Marshal(list)
	v, _ := jsonx.Parse(b)
	return jsonx.Canonical(v)
}

func TestGoldenInbound(t *testing.T) {
	g := loadGolden(t)
	e := setup(t)
	e.fetch = func(url string) ([]byte, error) {
		name := url[len(url)-len("huge1"):]
		if name == "huge1" {
			b := make([]byte, 2*1024*1024)
			for i := range b {
				b[i] = 'A'
			}
			return b, nil
		}
		parts := []byte(url)
		i := len(parts) - 1
		for i >= 0 && parts[i] != '/' {
			i--
		}
		return []byte("bytes:" + string(parts[i+1:])), nil
	}
	e.start(t)
	for _, step := range g.TgInbound {
		if step.Claim != nil {
			c, err := owner.CreateClaim(e.ctx.DB, "telegram", time.Now().UnixMilli())
			testutil.Must(t, err)
			name := "Owner"
			_, err = owner.ClaimOwner(e.ctx.DB, owner.ClaimInput{Channel: "telegram", Nonce: c.Nonce, ExternalID: jsonx.NumberString(float64(*step.Claim)), DisplayName: &name}, time.Now().UnixMilli())
			testutil.Must(t, err)
			continue
		}
		inBefore, callsBefore, fetchedBefore := e.ctx.InboundCount(), len(e.fake.Calls()), len(e.fetched)
		e.fake.FeedJSON(string(step.Update))
		var got []any
		for _, m := range e.ctx.Inbound()[inBefore:] {
			if m.Callback != nil {
				m.At = 0
			}
			b, _ := json.Marshal(m)
			var o map[string]any
			_ = json.Unmarshal(b, &o)
			got = append(got, o)
		}
		if got == nil {
			got = []any{}
		}
		want := step.Inbound
		if want == nil {
			want = []any{}
		}
		if g, w := normInbound(got), normInbound(want); g != w {
			t.Errorf("%s: inbound\n got  %s\n want %s", step.Name, g, w)
		}
		if g, w := callsJSON(e.fake.Calls()[callsBefore:]), ordered(t, step.Calls); g != w {
			t.Errorf("%s: calls\n got  %s\n want %s", step.Name, g, w)
		}
		if g, w := jsonx.Stringify(e.fetched[fetchedBefore:]), jsonx.Stringify(step.Fetched); g != w && !(len(step.Fetched) == 0 && len(e.fetched) == fetchedBefore) {
			t.Errorf("%s: fetched %s, want %s", step.Name, g, w)
		}
	}
}
