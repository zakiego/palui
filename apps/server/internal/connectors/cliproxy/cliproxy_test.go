package cliproxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/cliproxy"
	"github.com/zakiego/palui/apps/server/internal/connectors/conntest"
	"github.com/zakiego/palui/apps/server/internal/connectors/gateway"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/settings"
	"github.com/zakiego/palui/apps/server/internal/testutil"
)

type call struct {
	URL    string
	Header http.Header
	Body   map[string]any
}

type fake struct {
	mu    sync.Mutex
	calls []call
	fail  bool
}

func (f *fake) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.fail {
		return nil, errors.New("ECONNREFUSED")
	}
	var body map[string]any
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
	}
	u := r.URL.String()
	f.mu.Lock()
	f.calls = append(f.calls, call{u, r.Header.Clone(), body})
	f.mu.Unlock()
	reply := func(v any) (*http.Response, error) {
		b, _ := json.Marshal(v)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
	}
	sse := func(events ...string) (*http.Response, error) {
		var b strings.Builder
		for _, e := range events {
			b.WriteString("data: " + e + "\n\n")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(b.String())), Request: r}, nil
	}
	switch {
	case strings.HasSuffix(u, "/v1/models"):
		return reply(map[string]any{"data": []any{map[string]any{"id": "claude-sonnet-4-6"}, map[string]any{"id": "gemini-3-flash-preview"}}})
	case strings.HasSuffix(u, "/v1/messages"):
		return sse(
			`{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"x","content":[],"stop_reason":null,"usage":{"input_tokens":3,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		)
	case strings.HasSuffix(u, "/v1/chat/completions"):
		return sse(
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"x","choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`,
			`[DONE]`,
		)
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("nope")), Request: r}, nil
}

func (f *fake) Calls() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func ctx(t *testing.T, id, config string, secrets map[string]string) connectors.Context {
	o, err := jsonx.ParseObject([]byte(config))
	testutil.Must(t, err)
	c := conntest.Ctx(t, nil, id, o, secrets)
	link := func(c, m string) settings.ModelChainLink { return settings.ModelChainLink{ConnectorID: c, Model: m} }
	testutil.Must(t, settings.Set(c.DB, "modelRoles", settings.ModelRoles{
		Default: []settings.ModelChainLink{link(id, "claude-sonnet-4-6")},
		Fast:    []settings.ModelChainLink{link(id, "gemini-3-flash-preview")},
		Vision:  []settings.ModelChainLink{link("other", "x")},
	}, 1))
	return c
}

func TestTestListsModelsThenMakesA1TokenCallPerRoleItServes(t *testing.T) {
	f := &fake{}
	rt := cliproxy.GatewayRuntime(cliproxy.Manifest, &http.Client{Transport: f})
	r, err := rt.Test(ctx(t, "cliproxy", `{"baseUrl":"http://proxy:8317/","protocols":{"fast":"openai"}}`, map[string]string{"apiKey": "secret-key"}))
	testutil.Must(t, err)
	if !r.OK {
		t.Fatalf("%+v", r)
	}
	data := conntest.Of(r.Data)
	if data.JSON("models") != `["claude-sonnet-4-6","gemini-3-flash-preview"]` || data.JSON("roles") != `{"default":"claude-sonnet-4-6: ok","fast":"gemini-3-flash-preview: ok"}` {
		t.Fatal(data)
	}
	calls := f.Calls()
	var urls []string
	for _, c := range calls {
		urls = append(urls, c.URL)
	}
	if strings.Join(urls, ",") != "http://proxy:8317/v1/models,http://proxy:8317/v1/messages,http://proxy:8317/v1/chat/completions" {
		t.Fatal(urls)
	}
	if calls[1].Header.Get("x-api-key") != "secret-key" || calls[2].Header.Get("authorization") != "Bearer secret-key" || calls[1].Body["max_tokens"] != float64(1) {
		t.Fatalf("%+v %+v", calls[1], calls[2])
	}
}

func TestModelGatewayNeedsABaseURLAndReportsAnUnreachableOne(t *testing.T) {
	if _, err := gateway.Manifest.ParseConfig(jsonx.NewMap[any]()); err == nil {
		t.Fatal("a gateway without a base URL was accepted")
	}
	rt := cliproxy.GatewayRuntime(gateway.Manifest, &http.Client{Transport: &fake{fail: true}})
	r, err := rt.Test(ctx(t, "gateway-llm", `{"baseUrl":"https://llm.example.com"}`, map[string]string{"apiKey": "sk-x"}))
	testutil.Must(t, err)
	if r.OK || !strings.Contains(r.Detail, "https://llm.example.com") {
		t.Fatalf("%+v", r)
	}
	ok := func(k string) bool {
		m := jsonx.NewMap[string]()
		m.Set("apiKey", k)
		return gateway.Manifest.ParseSecrets(m) == nil
	}
	if ok("") || !ok("sk-anything") {
		t.Fatal("key check")
	}
}

func TestStartBuildsModelsPerRole(t *testing.T) {
	h, err := cliproxy.Runtime.Start(ctx(t, "cliproxy", `{"protocols":{"vision":"openai"}}`, map[string]string{"apiKey": "k"}))
	testutil.Must(t, err)
	def, _ := h.Model("claude-sonnet-4-6", "default")
	vis, _ := h.Model("gemini", "vision")
	if !strings.Contains(def.Provider(), "anthropic") || !strings.Contains(vis.Provider(), "cliproxy") {
		t.Fatal(def.Provider(), vis.Provider())
	}
	if conntest.Of(h.Live()).S("baseUrl") != "http://host.docker.internal:8317" {
		t.Fatal(h.Live())
	}
}

func TestStartListsTheGatewaysModelsThroughTheListModelsAction(t *testing.T) {
	f := &fake{}
	h, err := cliproxy.GatewayRuntime(cliproxy.Manifest, &http.Client{Transport: f}).Start(ctx(t, "cliproxy", `{}`, map[string]string{"apiKey": "k"}))
	testutil.Must(t, err)
	res, err := h.Action(context.Background(), "listModels", nil)
	testutil.Must(t, err)
	if jsonx.Stringify(res) != `{"models":["claude-sonnet-4-6","gemini-3-flash-preview"]}` {
		t.Fatal(jsonx.Stringify(res))
	}
	if _, err := h.Action(context.Background(), "nope", nil); err == nil || err.Error() != "Unknown action nope" {
		t.Fatal(err)
	}
}

func TestALinksEffortReachesTheGatewayAsReasoningEffortAndOnlyOnThatLink(t *testing.T) {
	f := &fake{}
	h, err := cliproxy.GatewayRuntime(cliproxy.Manifest, &http.Client{Transport: f}).Start(ctx(t, "cliproxy", `{"protocols":{"default":"openai"}}`, map[string]string{"apiKey": "k"}))
	testutil.Must(t, err)
	lm, _ := h.Model("gpt-5", "default")
	run := func(effort string) {
		m, err := models.NewFailoverModel([]models.ModelLink{{ConnectorID: "cliproxy", Model: "gpt-5", Effort: effort, LM: lm}}, nil)
		testutil.Must(t, err)
		_, err = models.Generate(context.Background(), m, models.CallOptions{Prompt: []models.Message{models.User("hi")}})
		testutil.Must(t, err)
	}
	run("high")
	run("")
	var bodies []map[string]any
	for _, c := range f.Calls() {
		if strings.HasSuffix(c.URL, "/chat/completions") {
			bodies = append(bodies, c.Body)
		}
	}
	if bodies[0]["reasoning_effort"] != "high" {
		t.Fatal(bodies[0])
	}
	if _, ok := bodies[1]["reasoning_effort"]; ok {
		t.Fatal(bodies[1])
	}
}

var _ = kit.O

func TestCacheBreakpointsReachTheGatewayOnTheAnthropicProtocolAndNeverOnTheOpenAIOne(t *testing.T) {
	f := &fake{}
	h, err := cliproxy.GatewayRuntime(cliproxy.Manifest, &http.Client{Transport: f}).Start(ctx(t, "cliproxy", `{"protocols":{"fast":"openai"}}`, map[string]string{"apiKey": "k"}))
	testutil.Must(t, err)
	now := int64(1794377100000) // 2026-11-11T06:05:00Z
	prompt := append(chat.SystemBlocks("You are Palui.", "talked about kopi"), chat.WithTurnContext([]models.Message{models.UserParts(models.TextPart("jam berapa?"))}, chat.BuildTurnContext(chat.TurnContextInput{Now: now, TZ: "Asia/Makassar"}), true)...)
	run := func(model, role string) {
		lm, _ := h.Model(model, role)
		m, err := models.NewFailoverModel([]models.ModelLink{{ConnectorID: "cliproxy", Model: model, LM: lm}}, nil)
		testutil.Must(t, err)
		_, err = models.Generate(context.Background(), m, models.CallOptions{Prompt: prompt})
		testutil.Must(t, err)
	}
	run("claude-sonnet-4-6", "default")
	run("gpt-5", "fast")
	var anthropic, openai map[string]any
	for _, c := range f.Calls() {
		if strings.HasSuffix(c.URL, "/v1/messages") {
			anthropic = c.Body
		}
		if strings.HasSuffix(c.URL, "/v1/chat/completions") {
			openai = c.Body
		}
	}
	sys, _ := json.Marshal(anthropic["system"])
	if string(sys) != `[{"cache_control":{"ttl":"1h","type":"ephemeral"},"text":"You are Palui.","type":"text"},{"text":"Summary of the earlier conversation:\ntalked about kopi","type":"text"}]` {
		t.Fatal(string(sys))
	}
	content := anthropic["messages"].([]any)[0].(map[string]any)["content"].([]any)
	first, second := content[0].(map[string]any), content[1].(map[string]any)
	if cc, _ := json.Marshal(first["cache_control"]); string(cc) != `{"ttl":"1h","type":"ephemeral"}` {
		t.Fatal(string(cc))
	}
	if _, ok := second["cache_control"]; ok || !strings.Contains(second["text"].(string), "Now: Rabu, 11 November 2026 14.05 WITA") {
		t.Fatal(second)
	}
	o, _ := json.Marshal(openai)
	for _, bad := range []string{"cache_control", "cacheControl", "palui"} {
		if strings.Contains(string(o), bad) {
			t.Fatal(bad, string(o))
		}
	}
	if !strings.Contains(string(o), "jam berapa?") {
		t.Fatal(string(o))
	}
}
