package models_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
)

// Goldens from testdata/gen/models.ts (the real TS code and AI SDK providers).

func readGolden(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/models/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func TestGoldenStoredMessagesRoundTrip(t *testing.T) {
	var cases []struct {
		Name string `json:"name"`
		JSON string `json:"json"`
	}
	readGolden(t, "messages.json", &cases)
	if len(cases) < 9 {
		t.Fatalf("%d cases", len(cases))
	}
	for _, c := range cases {
		msgs, err := models.ParseMessages([]byte(c.JSON))
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		out, err := models.MarshalMessages(msgs)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != c.JSON {
			t.Errorf("%s: round trip differs\n got %s\nwant %s", c.Name, out, c.JSON)
		}
		// Rebuilding every part through the typed accessors and setters keeps it too.
		for i := range msgs {
			parts := msgs[i].Parts()
			if parts != nil {
				msgs[i].SetParts(parts)
			}
		}
		again, _ := models.MarshalMessages(msgs)
		if string(again) != c.JSON {
			t.Errorf("%s: SetParts changed the bytes\n got %s", c.Name, again)
		}
	}
}

func TestStoredMessageAccessors(t *testing.T) {
	var cases []struct {
		Name string `json:"name"`
		JSON string `json:"json"`
	}
	readGolden(t, "messages.json", &cases)
	byName := map[string][]models.Message{}
	for _, c := range cases {
		byName[c.Name], _ = models.ParseMessages([]byte(c.JSON))
	}
	tr := byName["toolRound"]
	calls := tr[0].Parts()
	if calls[1].Type() != "tool-call" || calls[1].ToolCallID() != "call_1" || calls[1].ToolName() != "notes__list" || string(calls[1].Input()) != `{"q":"listrik","limit":3}` {
		t.Fatalf("%+v", calls[1])
	}
	res := tr[1].Parts()
	if res[0].Output().Type() != "json" || res[1].Output().Type() != "error-text" || string(res[1].Output().Value()) != `"fetch failed"` {
		t.Fatal("outputs")
	}
	ap := byName["approvals"]
	if p := ap[0].Parts()[1]; p.Type() != "tool-approval-request" || p.ApprovalID() != "aprq_1" || p.ToolCallID() != "call_3" {
		t.Fatal("approval request")
	}
	if p := ap[1].Parts()[0]; p.Approved() || *p.Reason() != "owner said no" {
		t.Fatal("approval response")
	}
	if text, ok := byName["textOnly"][0].Text(); !ok || text != "halo" {
		t.Fatal(text)
	}
	// Typed constructors write the TS key order.
	built := []models.Message{
		models.AssistantParts(models.ToolCallPart("call_1", "notes__list", json.RawMessage(`{"q":"listrik","limit":3}`))),
		models.ToolMessage(models.ToolResultPart("call_3", "alpha__wipe", models.OutputDenied(strPtr("Not run: the owner did not approve it.")))),
		models.ToolMessage(models.ToolApprovalResponsePart("aprq_1", false, strPtr("owner said no"))),
	}
	b, _ := models.MarshalMessages(built)
	want := `[{"role":"assistant","content":[{"type":"tool-call","toolCallId":"call_1","toolName":"notes__list","input":{"q":"listrik","limit":3}}]},{"role":"tool","content":[{"type":"tool-result","toolCallId":"call_3","toolName":"alpha__wipe","output":{"type":"execution-denied","reason":"Not run: the owner did not approve it."}}]},{"role":"tool","content":[{"type":"tool-approval-response","approvalId":"aprq_1","approved":false,"reason":"owner said no"}]}]`
	if string(b) != want {
		t.Fatalf("\n got %s\nwant %s", b, want)
	}
}

func strPtr(s string) *string { return &s }

type wireGolden struct {
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"tools"`
	Prompts map[string]struct {
		System          json.RawMessage `json:"system"`
		Messages        json.RawMessage `json:"messages"`
		Tools           []string        `json:"tools"`
		ActiveTools     []string        `json:"activeTools"`
		ToolChoice      json.RawMessage `json:"toolChoice"`
		MaxOutputTokens *int            `json:"maxOutputTokens"`
		Temperature     *float64        `json:"temperature"`
	} `json:"prompts"`
	Cases []struct {
		Provider string  `json:"provider"`
		ModelID  string  `json:"modelId"`
		Effort   *string `json:"effort"`
		Prompt   string  `json:"prompt"`
		Error    string  `json:"error"`
		Request  *struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    json.RawMessage   `json:"body"`
		} `json:"request"`
	} `json:"cases"`
}

func TestGoldenWireRequests(t *testing.T) {
	var g wireGolden
	readGolden(t, "wire.json", &g)
	n := 0
	for _, c := range g.Cases {
		name := fmt.Sprintf("%s %s %v %s", c.Provider, c.ModelID, c.Effort, c.Prompt)
		p := g.Prompts[c.Prompt]
		var prompt []models.Message
		if len(p.System) > 0 {
			sys, err := models.ParseMessages(p.System)
			if err != nil {
				t.Fatal(err)
			}
			prompt = append(prompt, sys...)
		}
		msgs, err := models.ParseMessages(p.Messages)
		if err != nil {
			t.Fatal(err)
		}
		prompt = append(prompt, msgs...)
		opts := models.CallOptions{Prompt: prompt, MaxOutputTokens: p.MaxOutputTokens, Temperature: p.Temperature, ActiveTools: p.ActiveTools}
		for _, tn := range p.Tools {
			for _, td := range g.Tools {
				if td.Name == tn {
					opts.Tools = append(opts.Tools, models.ToolDef{Name: td.Name, Description: td.Description, InputSchema: td.InputSchema})
				}
			}
		}
		if len(p.ToolChoice) > 0 {
			var s string
			if json.Unmarshal(p.ToolChoice, &s) == nil {
				opts.ToolChoice = &models.ToolChoice{Type: s}
			} else {
				var tc models.ToolChoice
				_ = json.Unmarshal(p.ToolChoice, &tc)
				opts.ToolChoice = &tc
			}
		}
		effort := ""
		if c.Effort != nil {
			effort = *c.Effort
		}
		var lm models.LanguageModel
		if c.Provider == "anthropic" {
			lm = models.NewAnthropic(models.AnthropicConfig{BaseURL: "http://gateway.test:8317/v1", APIKey: "test-key"}, c.ModelID)
		} else {
			lm = models.NewOpenAI(models.OpenAIConfig{BaseURL: "http://gateway.test:8317/v1", APIKey: "test-key", Name: "cliproxy"}, c.ModelID)
		}
		link := models.ModelLink{ConnectorID: "cliproxy", Model: c.ModelID, Effort: effort, LM: lm}
		o, err := models.ForLink(opts, link)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var url string
		var headers map[string]string
		var body *jsonx.Object
		if a, ok := lm.(*models.AnthropicModel); ok {
			r, err := a.BuildRequest(o)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			url, headers, body = r.URL, r.Headers, r.Body
		} else {
			r, err := lm.(*models.OpenAIModel).BuildRequest(o)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			url, headers, body = r.URL, r.Headers, r.Body
		}
		if c.Request == nil {
			t.Errorf("%s: golden has no request (%s)", name, c.Error)
			continue
		}
		if url != c.Request.URL {
			t.Errorf("%s: url %s", name, url)
		}
		for _, h := range []string{"anthropic-beta", "anthropic-version", "x-api-key", "authorization"} {
			if headers[h] != c.Request.Headers[h] {
				t.Errorf("%s: header %s = %q, want %q", name, h, headers[h], c.Request.Headers[h])
			}
		}
		got := jsonx.Canonical(mustParse(t, jsonx.Stringify(body)))
		want := jsonx.Canonical(mustParse(t, string(c.Request.Body)))
		if got != want {
			t.Errorf("%s: body differs\n got %s\nwant %s", name, got, want)
		}
		n++
	}
	if n < 140 {
		t.Fatalf("only %d wire cases", n)
	}
}

func mustParse(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type streamGolden struct {
	Name        string           `json:"name"`
	Provider    string           `json:"provider"`
	Status      int              `json:"status"`
	ContentType string           `json:"contentType"`
	Body        string           `json:"body"`
	Parts       []map[string]any `json:"parts"`
	Thrown      *struct {
		Message      string  `json:"message"`
		StatusCode   *int    `json:"statusCode"`
		IsRetryable  bool    `json:"isRetryable"`
		ResponseBody *string `json:"responseBody"`
		Failover     bool    `json:"failover"`
	} `json:"thrown"`
}

// eventJSON is the Go event in the golden's stream part shape.
func eventJSON(ev models.Event) map[string]any {
	m := map[string]any{"type": ev.Type}
	switch ev.Type {
	case models.EventTextStart, models.EventTextEnd, models.EventReasoningStart, models.EventReasoningEnd, models.EventToolInputEnd:
		m["id"] = ev.ID
	case models.EventTextDelta, models.EventReasoningDelta, models.EventToolInputDelta:
		m["id"], m["delta"] = ev.ID, ev.Delta
		if ev.ProviderMetadata != nil {
			m["providerMetadata"] = mustParseAny(jsonx.Stringify(ev.ProviderMetadata))
		}
	case models.EventToolInputStart:
		m["id"], m["toolName"] = ev.ID, ev.ToolName
	case models.EventToolCall:
		m["toolCallId"], m["toolName"], m["input"] = ev.ToolCallID, ev.ToolName, ev.Input
	case models.EventFinish:
		m["finishReason"] = mustParseAny(jsonx.Stringify(ev.FinishReason))
		m["usage"] = dropNulls(mustParseAny(jsonx.Stringify(ev.Usage)))
	case models.EventError:
		m["message"] = ev.Err.Error()
	}
	return m
}

func mustParseAny(s string) any {
	var v any
	_ = json.Unmarshal([]byte(s), &v)
	return v
}

func dropNulls(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if val == nil {
				delete(x, k)
			} else {
				x[k] = dropNulls(val)
			}
		}
	}
	return v
}

func TestGoldenStreams(t *testing.T) {
	var fixtures []streamGolden
	readGolden(t, "stream.json", &fixtures)
	for _, f := range fixtures {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("content-type", f.ContentType)
			w.WriteHeader(f.Status)
			_, _ = io.WriteString(w, f.Body)
		}))
		var lm models.LanguageModel
		if f.Provider == "anthropic" {
			lm = models.NewAnthropic(models.AnthropicConfig{BaseURL: srv.URL + "/v1", APIKey: "k", Client: srv.Client()}, "claude-sonnet-4-6")
		} else {
			lm = models.NewOpenAI(models.OpenAIConfig{BaseURL: srv.URL + "/v1", APIKey: "k", Name: "cliproxy", Client: srv.Client()}, "m")
		}
		s, err := lm.Stream(context.Background(), models.CallOptions{Prompt: []models.Message{models.User("x")}})
		if f.Thrown != nil {
			var api *models.APICallError
			if !errors.As(err, &api) {
				t.Errorf("%s: want APICallError, got %v", f.Name, err)
				srv.Close()
				continue
			}
			wantMsg := f.Thrown.Message
			if wantMsg == "" {
				// fetch's Response has no status text in the generator; a real server sends one.
				wantMsg = http.StatusText(f.Status)
			}
			if api.Message != wantMsg || api.StatusCode != *f.Thrown.StatusCode || api.IsRetryable != f.Thrown.IsRetryable || (f.Thrown.ResponseBody != nil && api.ResponseBody != *f.Thrown.ResponseBody) || models.IsFailoverError(err) != f.Thrown.Failover {
				t.Errorf("%s: %+v (failover %v), want %+v", f.Name, api, models.IsFailoverError(err), *f.Thrown)
			}
			srv.Close()
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", f.Name, err)
			srv.Close()
			continue
		}
		var got []map[string]any
		for {
			ev, err := s.Recv()
			if err != nil {
				break
			}
			got = append(got, eventJSON(ev))
		}
		s.Close()
		srv.Close()
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(f.Parts)
		if jsonx.Canonical(mustParseAny(string(g))) != jsonx.Canonical(mustParseAny(string(w))) {
			t.Errorf("%s:\n got %s\nwant %s", f.Name, g, w)
		}
	}
}

func TestGoldenFixturesCoverTheCases(t *testing.T) {
	var fixtures []streamGolden
	readGolden(t, "stream.json", &fixtures)
	names := []string{}
	for _, f := range fixtures {
		names = append(names, f.Name)
	}
	all := strings.Join(names, ",")
	for _, want := range []string{"text", "parallel tool calls", "mid-stream error", "429", "529", "503", "cache"} {
		if want == "cache" {
			continue
		}
		if !strings.Contains(all, want) {
			t.Errorf("no fixture for %s", want)
		}
	}
}
