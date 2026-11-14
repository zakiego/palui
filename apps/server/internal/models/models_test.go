package models_test

// Ports of models/cache.test.ts and models/meter.test.ts, plus failover, chain
// resolution, stream lifecycle and the scripted model.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/palui/apps/server/internal/budget"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

func msgs(t *testing.T, s string) []models.Message {
	t.Helper()
	m, err := models.ParseMessages([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

const mark1h = `{"palui":{"cache":"1h"}}`

// A turn's first step: marked system block, summary, history, newest user message with the per-turn block.
func firstStep() string {
	return `[{"role":"system","content":"stable","providerOptions":` + mark1h + `},{"role":"system","content":"summary"},{"role":"user","content":[{"type":"text","text":"halo"}]},{"role":"assistant","content":[{"type":"text","text":"hai"}]},{"role":"user","content":[{"type":"text","text":"jam berapa?","providerOptions":` + mark1h + `},{"type":"text","text":"[Palui context]\nNow: 14.05"}]}]`
}

func secondStep() string {
	return strings.TrimSuffix(firstStep(), "]") + `,{"role":"assistant","content":[{"type":"tool-call","toolCallId":"c1","toolName":"clock__now","input":{}}]},{"role":"tool","content":[{"type":"tool-result","toolCallId":"c1","toolName":"clock__now","output":{"type":"text","value":"14.05"}},{"type":"tool-result","toolCallId":"c2","toolName":"clock__now","output":{"type":"text","value":"14.05"}}]}]`
}

func promptJSON(o models.CallOptions) string { return models.PromptText(o.Prompt) }

func breakpoints(o models.CallOptions) int { return strings.Count(promptJSON(o), `"cacheControl"`) }

const (
	long  = `{"anthropic":{"cacheControl":{"type":"ephemeral","ttl":"1h"}}}`
	short = `{"anthropic":{"cacheControl":{"type":"ephemeral"}}}`
)

func po(v interface{ ProviderOptions() *jsonx.Object }) string {
	p := v.ProviderOptions()
	if p == nil {
		return ""
	}
	return jsonx.Stringify(p)
}

func TestPromptCache(t *testing.T) {
	t.Run("knows the anthropic protocol by its provider name", func(t *testing.T) {
		for p, want := range map[string]bool{"anthropic.messages": true, "anthropic": true, "cliproxy.chat": false, "anthropicish": false} {
			if models.SpeaksAnthropic(p) != want {
				t.Fatal(p)
			}
		}
	})
	t.Run("system block and newest user message get hour-long breakpoints", func(t *testing.T) {
		out := models.WithPromptCache(models.CallOptions{Prompt: msgs(t, firstStep())}, "anthropic.messages")
		if po(out.Prompt[0]) != long || po(out.Prompt[1]) != "" {
			t.Fatal(po(out.Prompt[0]))
		}
		parts := out.Prompt[4].Parts()
		if jsonx.Stringify(parts) != `[{"type":"text","text":"jam berapa?","providerOptions":`+long+`},{"type":"text","text":"[Palui context]\nNow: 14.05"}]` {
			t.Fatal(jsonx.Stringify(parts))
		}
		if breakpoints(out) != 2 || strings.Contains(promptJSON(out), "palui") {
			t.Fatal(promptJSON(out))
		}
	})
	t.Run("short breakpoint on the last tool result from the second step on", func(t *testing.T) {
		out := models.WithPromptCache(models.CallOptions{Prompt: msgs(t, secondStep())}, "anthropic.messages")
		tool := out.Prompt[len(out.Prompt)-1].Parts()
		if po(tool[0]) != "" || po(tool[1]) != short || breakpoints(out) != 3 {
			t.Fatal(promptJSON(out))
		}
	})
	t.Run("skips approval responses when looking for the last tool result", func(t *testing.T) {
		p := strings.TrimSuffix(secondStep(), "]") + `,{"role":"tool","content":[{"type":"tool-result","toolCallId":"c3","toolName":"t","output":{"type":"text","value":"x"}},{"type":"tool-approval-response","approvalId":"a","approved":true}]}]`
		out := models.WithPromptCache(models.CallOptions{Prompt: msgs(t, p)}, "anthropic.messages")
		tool := out.Prompt[len(out.Prompt)-1].Parts()
		if po(tool[0]) != short || po(tool[1]) != "" {
			t.Fatal(promptJSON(out))
		}
	})
	t.Run("keeps other provider options next to the breakpoint", func(t *testing.T) {
		p := `[{"role":"system","content":"s","providerOptions":{"palui":{"cache":"5m"},"anthropic":{"foo":1},"other":{"bar":2}}}]`
		out := models.WithPromptCache(models.CallOptions{Prompt: msgs(t, p)}, "anthropic.messages")
		if got := po(out.Prompt[0]); jsonx.Canonical(mustParse(t, got)) != jsonx.Canonical(mustParse(t, `{"anthropic":{"foo":1,"cacheControl":{"type":"ephemeral"}},"other":{"bar":2}}`)) {
			t.Fatal(got)
		}
	})
	t.Run("sends the openai protocol nothing", func(t *testing.T) {
		for _, step := range []string{firstStep(), secondStep()} {
			out := models.WithPromptCache(models.CallOptions{Prompt: msgs(t, step)}, "cliproxy.chat")
			text := promptJSON(out)
			if strings.Contains(text, "cacheControl") || strings.Contains(text, "anthropic") || strings.Contains(text, "palui") {
				t.Fatal(text)
			}
			if text != strings.ReplaceAll(strings.ReplaceAll(step, `,"providerOptions":`+mark1h, ""), "\n", "\\n") && text != stripMarks(step) {
				t.Fatalf("\n got %s\nwant %s", text, stripMarks(step))
			}
		}
	})
	t.Run("leaves a prompt without marks alone", func(t *testing.T) {
		p := msgs(t, `[{"role":"system","content":"s"},{"role":"user","content":[{"type":"text","text":"x"}]},{"role":"tool","content":[{"type":"tool-result","toolCallId":"c","toolName":"t","output":{"type":"text","value":"x"}}]}]`)
		for _, prov := range []string{"anthropic.messages", "cliproxy.chat"} {
			out := models.WithPromptCache(models.CallOptions{Prompt: p}, prov)
			if &out.Prompt[0] != &p[0] {
				t.Fatal("copied")
			}
		}
	})
	t.Run("does not change the options it was given", func(t *testing.T) {
		o := models.CallOptions{Prompt: msgs(t, secondStep())}
		before := promptJSON(o)
		models.WithPromptCache(o, "anthropic.messages")
		if promptJSON(o) != before {
			t.Fatal("mutated")
		}
	})
}

func stripMarks(s string) string {
	m, _ := models.ParseMessages([]byte(s))
	for i := range m {
		m[i].Delete("providerOptions")
		if parts := m[i].Parts(); parts != nil {
			for j := range parts {
				parts[j].Delete("providerOptions")
			}
			m[i].SetParts(parts)
		}
	}
	return models.PromptText(m)
}

func drain(t *testing.T, s models.Stream) []models.Event {
	t.Helper()
	var out []models.Event
	for {
		ev, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
}

func TestMeteredModel(t *testing.T) {
	t.Run("reports tokens from every call", func(t *testing.T) {
		var seen []int64
		m := models.NewScripted(models.Say("a"), models.Say("b"))
		metered := &models.MeteredModel{Model: m, OnTokens: func(n int64) { seen = append(seen, n) }}
		if _, err := models.Generate(context.Background(), metered, models.CallOptions{Prompt: []models.Message{models.User("x")}}); err != nil {
			t.Fatal(err)
		}
		s, _ := metered.Stream(context.Background(), models.CallOptions{Prompt: []models.Message{models.User("y")}})
		drain(t, s)
		// scriptedModel reports 10 input + 5 output tokens per call.
		if len(seen) != 2 || seen[0] != 15 || seen[1] != 15 {
			t.Fatal(seen)
		}
	})
	t.Run("adds parse and compaction tokens to the daily cap counter", func(t *testing.T) {
		d := testutil.DB(t)
		now := time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC).UnixMilli()
		m := models.MeterToBudget(d, models.NewScripted(models.Say("summary")), func() int64 { return now })
		if _, err := models.Generate(context.Background(), m, models.CallOptions{Prompt: []models.Message{models.User("x")}}); err != nil {
			t.Fatal(err)
		}
		if n, _ := budget.TokensToday(d, now); n != 15 {
			t.Fatal(n)
		}
	})
	t.Run("counts cache reads at a tenth", func(t *testing.T) {
		p := func(v int64) *int64 { return &v }
		if n := models.UsageTokens(&models.Usage{InputTokens: models.InputTokens{Total: p(1000), CacheRead: p(800)}, OutputTokens: models.OutputTokens{Total: p(50)}}); n != 200+80+50 {
			t.Fatal(n)
		}
		if n := models.UsageTokens(&models.Usage{InputTokens: models.InputTokens{Total: p(1000)}, OutputTokens: models.OutputTokens{Total: p(50)}}); n != 1050 {
			t.Fatal(n)
		}
		if models.UsageTokens(nil) != 0 {
			t.Fatal("nil")
		}
	})
}

func TestIsFailoverError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{models.NewHTTPError(429), true},
		{models.NewHTTPError(500), true},
		{models.NewHTTPError(529), true},
		{models.NewHTTPError(400), false},
		{models.NewHTTPError(401), false},
		{&models.APICallError{Message: "Your credit balance is too low", StatusCode: 403}, true},
		{&models.APICallError{Message: "forbidden", StatusCode: 403}, false},
		{&models.APICallError{Message: "x", StatusCode: 402, ResponseBody: `{"error":"quota exhausted"}`}, true},
		{&models.APICallError{Message: "Cannot connect to API: dial tcp", StatusCode: 0}, true},
		{context.Canceled, false},
		{context.DeadlineExceeded, false},
		{errors.New("rate limit reached"), true},
		{errors.New("model overloaded"), true},
		{errors.New("fetch failed"), true},
		{errors.New("bad input"), false},
		{wrapErr{errors.New("outer"), models.NewHTTPError(503)}, true},
		{nil, false},
	}
	for _, c := range cases {
		if got := models.IsFailoverError(c.err); got != c.want {
			t.Errorf("%v: %v", c.err, got)
		}
	}
}

type wrapErr struct{ outer, cause error }

func (w wrapErr) Error() string { return w.outer.Error() }
func (w wrapErr) Unwrap() error { return w.cause }

func TestFailoverModel(t *testing.T) {
	ctx := context.Background()
	if _, err := models.NewFailoverModel(nil, nil); err == nil || err.Error() != "No model connector is running" {
		t.Fatal(err)
	}
	a := models.NewScripted(models.Fail(models.NewHTTPError(429))).WithProvider("anthropic.messages")
	b := models.NewScripted(models.Say("dari b")).WithProvider("cliproxy.chat")
	var failed []string
	fm, _ := models.NewFailoverModel([]models.ModelLink{{ConnectorID: "a", Model: "ma", Effort: "high", LM: a}, {ConnectorID: "b", Model: "mb", LM: b}}, func(from models.ModelLink, err error) { failed = append(failed, from.ConnectorID) })
	if fm.ModelID() != "ma" || fm.Provider() != "anthropic.messages" || fm.Served() != nil {
		t.Fatal("before any call")
	}
	p := msgs(t, firstStep())
	r, err := models.Generate(ctx, fm, models.CallOptions{Prompt: p})
	if err != nil || r.Text != "dari b" {
		t.Fatal(r, err)
	}
	if len(failed) != 1 || failed[0] != "a" || fm.Served().ConnectorID != "b" || fm.ModelID() != "mb" || fm.Provider() != "cliproxy.chat" {
		t.Fatal(failed, fm.Served())
	}
	// Link a was asked with its effort and anthropic breakpoints; link b without either.
	if a.Calls[0].Reasoning != "high" || !strings.Contains(models.PromptText(a.Prompts[0]), "cacheControl") {
		t.Fatal(a.Calls[0].Reasoning)
	}
	if b.Calls[0].Reasoning != "" || strings.Contains(models.PromptText(b.Prompts[0]), "palui") || strings.Contains(models.PromptText(b.Prompts[0]), "cacheControl") {
		t.Fatal(models.PromptText(b.Prompts[0]))
	}
	// A caller's own effort wins.
	c := models.NewScripted(models.Say("x"))
	fc, _ := models.NewFailoverModel([]models.ModelLink{{ConnectorID: "c", Model: "mc", Effort: "high", LM: c}}, nil)
	_, _ = models.Generate(ctx, fc, models.CallOptions{Prompt: p, Reasoning: "low"})
	if c.Calls[0].Reasoning != "low" {
		t.Fatal(c.Calls[0].Reasoning)
	}
	// A non-failover error stops at the first link; the last link's error is returned.
	d := models.NewScripted(models.Fail(models.NewHTTPError(400)))
	e := models.NewScripted(models.Say("never"))
	fd, _ := models.NewFailoverModel([]models.ModelLink{{ConnectorID: "d", LM: d}, {ConnectorID: "e", LM: e}}, nil)
	if _, err := fd.Stream(ctx, models.CallOptions{Prompt: p}); err == nil || e.Remaining() != 1 {
		t.Fatal(err)
	}
	g := models.NewScripted(models.Fail(models.NewHTTPError(503)))
	h := models.NewScripted(models.Fail(models.NewHTTPError(502)))
	fg, _ := models.NewFailoverModel([]models.ModelLink{{ConnectorID: "g", LM: g}, {ConnectorID: "h", LM: h}}, nil)
	var api *models.APICallError
	if _, err := fg.Stream(ctx, models.CallOptions{Prompt: p}); !errors.As(err, &api) || api.StatusCode != 502 {
		t.Fatal(err)
	}
}

type host map[string]func(model, role string) (models.LanguageModel, error)

func (h host) ConnectorModel(id, model, role string) (models.LanguageModel, bool, error) {
	f, ok := h[id]
	if !ok {
		return nil, false, nil
	}
	lm, err := f(model, role)
	return lm, true, err
}

func TestResolveChain(t *testing.T) {
	d := testutil.DB(t)
	testutil.Must(t, settings.Set(d, "modelRoles", settings.ModelRoles{
		Default: []settings.ModelChainLink{{ConnectorID: "cliproxy", Model: "m1", Effort: "low"}, {ConnectorID: "paused", Model: "m2"}, {ConnectorID: "broken", Model: "m3"}, {ConnectorID: "gw", Model: "m4"}},
		Fast:    []settings.ModelChainLink{},
		Vision:  []settings.ModelChainLink{{ConnectorID: "gw", Model: "v1"}},
	}, 1))
	var roles []string
	h := host{
		"cliproxy": func(m, r string) (models.LanguageModel, error) {
			roles = append(roles, r)
			return models.NewScripted(), nil
		},
		"gw":     func(m, r string) (models.LanguageModel, error) { return models.NewScripted(), nil },
		"broken": func(m, r string) (models.LanguageModel, error) { panic("cannot build") },
	}
	links, err := models.ResolveChain(d, h, "fast")
	if err != nil || len(links) != 2 || links[0].ConnectorID != "cliproxy" || links[0].Effort != "low" || links[1].Model != "m4" {
		t.Fatal(links, err)
	}
	if roles[0] != "fast" {
		t.Fatal(roles)
	}
	links, _ = models.ResolveChain(d, h, "vision")
	if len(links) != 1 || links[0].Model != "v1" {
		t.Fatal(links)
	}
}

func TestScriptedModel(t *testing.T) {
	ctx := context.Background()
	m := models.NewScripted(models.CallTool("notes__add", map[string]any{"title": "x"}, "call_1"), models.CallTools(models.ScriptedCall{ToolName: "a", Input: map[string]any{}, ToolCallID: "c1"}, models.ScriptedCall{ToolName: "b", Input: []int{1}, ToolCallID: "c2"}))
	tools := []models.ToolDef{{Name: "notes__add", InputSchema: json.RawMessage(`{}`)}, {Name: "web__fetch", InputSchema: json.RawMessage(`{}`)}}
	r, err := models.Generate(ctx, m, models.CallOptions{Prompt: []models.Message{models.User("x")}, Tools: tools, ActiveTools: []string{"web__fetch"}})
	if err != nil || len(r.ToolCalls) != 1 || r.ToolCalls[0].Input != `{"title":"x"}` || r.FinishReason.Unified != "tool-calls" {
		t.Fatal(r, err)
	}
	r, _ = models.Generate(ctx, m, models.CallOptions{Prompt: []models.Message{models.User("y")}, Tools: tools, ToolChoice: models.ChoiceNone})
	if len(r.ToolCalls) != 2 || r.ToolCalls[1].Input != "[1]" {
		t.Fatal(r)
	}
	r, _ = models.Generate(ctx, m, models.CallOptions{Prompt: []models.Message{models.User("z")}})
	if r.Text != "ok" || models.UsageTokens(&r.Usage) != 15 {
		t.Fatal(r)
	}
	if len(m.Prompts) != 3 || strings.Join(m.Offered[0], ",") != "web__fetch" || strings.Join(m.Offered[1], ",") != "notes__add,web__fetch" || m.Choices[0] != "auto" || m.Choices[1] != "none" || m.Choices[2] != "" {
		t.Fatal(m.Offered, m.Choices)
	}
	f := models.NewScripted(models.Fail(models.NewHTTPError(500)), models.Step{Fn: func(ctx context.Context, o models.CallOptions) ([]models.Event, error) {
		return models.Say("dynamic").Events, nil
	}})
	if _, err := f.Stream(ctx, models.CallOptions{}); err == nil || err.Error() != "HTTP 500" {
		t.Fatal(err)
	}
	if r, _ := models.Generate(ctx, f, models.CallOptions{}); r.Text != "dynamic" {
		t.Fatal(r)
	}
}

func TestStandardize(t *testing.T) {
	p := msgs(t, `[{"role":"user","content":"x"},{"role":"assistant","content":[{"type":"text","text":""},{"type":"tool-call","toolCallId":"c1","toolName":"t","input":{}},{"type":"tool-approval-request","approvalId":"a1","toolCallId":"c1"}]},{"role":"tool","content":[{"type":"tool-approval-response","approvalId":"a1","approved":true}]},{"role":"tool","content":[{"type":"tool-result","toolCallId":"c1","toolName":"t","output":{"type":"text","value":"ok"}}]}]`)
	out, err := models.Standardize(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"role":"user","content":[{"type":"text","text":"x"}]},{"role":"assistant","content":[{"type":"tool-call","toolCallId":"c1","toolName":"t","input":{}}]},{"role":"tool","content":[{"type":"tool-result","toolCallId":"c1","toolName":"t","output":{"type":"text","value":"ok"}}]}]`
	if models.PromptText(out) != want {
		t.Fatalf("\n got %s\nwant %s", models.PromptText(out), want)
	}
	_, err = models.Standardize(msgs(t, `[{"role":"assistant","content":[{"type":"tool-call","toolCallId":"c9","toolName":"t","input":{}}]},{"role":"user","content":"next"}]`))
	var missing *models.MissingToolResultsError
	if !errors.As(err, &missing) || missing.ToolCallIDs[0] != "c9" {
		t.Fatal(err)
	}
}

func TestStreamLifecycle(t *testing.T) {
	// A cancelled context ends the stream with the context error, and the
	// request sends the headers and body the providers need.
	release := make(chan struct{})
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, `event: message_start`+"\n"+`data: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":1}}}`+"\n\n"+`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	m := models.NewAnthropic(models.AnthropicConfig{BaseURL: srv.URL + "/v1/", APIKey: "sk-test", Client: srv.Client()}, "claude-sonnet-4-6")
	s, err := m.Stream(ctx, models.CallOptions{Prompt: []models.Message{models.User("x")}})
	if err != nil {
		t.Fatal(err)
	}
	if ev, _ := s.Recv(); ev.Type != models.EventTextStart {
		t.Fatal(ev)
	}
	cancel()
	ev, err := s.Recv()
	if err != nil || ev.Type != models.EventError || !errors.Is(ev.Err, context.Canceled) {
		t.Fatal(ev, err)
	}
	_ = s.Close()
	_ = s.Close()
	if gotHeaders.Get("x-api-key") != "sk-test" || gotHeaders.Get("anthropic-version") != "2023-06-01" || gotHeaders.Get("user-agent") != "palui" {
		t.Fatal(gotHeaders)
	}
	// No response at all is an APICallError without a status: failover moves on.
	dead := models.NewOpenAI(models.OpenAIConfig{BaseURL: "http://127.0.0.1:1/v1", Name: "x"}, "m")
	_, err = dead.Stream(context.Background(), models.CallOptions{Prompt: []models.Message{models.User("x")}})
	var api *models.APICallError
	if !errors.As(err, &api) || api.StatusCode != 0 || !models.IsFailoverError(err) {
		t.Fatal(err)
	}
}
