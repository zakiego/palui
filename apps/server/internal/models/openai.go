package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// The OpenAI-compatible chat completions client (POST
// {baseURL}/chat/completions, streaming), sending what
// @ai-sdk/openai-compatible 3.0.87 sends.

type OpenAIConfig struct {
	// BaseURL including the version path, e.g. "http://host:8317/v1".
	BaseURL string
	APIKey  string
	// Provider name; the provider string is "<name>.chat".
	Name   string
	Client *http.Client
}

type OpenAIModel struct {
	cfg   OpenAIConfig
	model string
}

// NewOpenAI is createOpenAICompatible({ name, baseURL, apiKey, fetch }).chatModel(modelId).
func NewOpenAI(cfg OpenAIConfig, modelID string) *OpenAIModel {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &OpenAIModel{cfg: cfg, model: modelID}
}

func (m *OpenAIModel) Provider() string { return m.cfg.Name + ".chat" }
func (m *OpenAIModel) ModelID() string  { return m.model }

func dataURL(p Part) (string, error) {
	d := partFileData(p)
	if d.Type == "url" {
		return d.URL, nil
	}
	full, err := fullMediaType(p)
	if err != nil {
		return "", err
	}
	return "data:" + full + ";base64," + d.Data, nil
}

func openAIContentPart(p Part) (any, error) {
	switch p.Type() {
	case "text":
		return obj("type", "text", "text", p.Text()), nil
	case "file":
		d := partFileData(p)
		mt := p.MediaType()
		if d.Type == "text" {
			if topLevel(mt) == "text" {
				return obj("type", "text", "text", d.Text), nil
			}
			return nil, &UnsupportedError{Functionality: "text file parts"}
		}
		switch topLevel(mt) {
		case "image":
			u, err := dataURL(p)
			if err != nil {
				return nil, err
			}
			return obj("type", "image_url", "image_url", obj("url", u)), nil
		case "video":
			u, err := dataURL(p)
			if err != nil {
				return nil, err
			}
			return obj("type", "video_url", "video_url", obj("url", u)), nil
		case "audio":
			if d.Type == "url" {
				return nil, &UnsupportedError{Functionality: "audio file parts with URLs"}
			}
			full, err := fullMediaType(p)
			if err != nil {
				return nil, err
			}
			format := map[string]string{"audio/wav": "wav", "audio/mp3": "mp3", "audio/mpeg": "mp3"}[full]
			if format == "" {
				return nil, &UnsupportedError{Functionality: "audio media type " + full}
			}
			return obj("type", "input_audio", "input_audio", obj("data", d.Data, "format", format)), nil
		case "application":
			if d.Type == "url" {
				return nil, &UnsupportedError{Functionality: "PDF file parts with URLs"}
			}
			full, err := fullMediaType(p)
			if err != nil {
				return nil, err
			}
			if full != "application/pdf" {
				return nil, &UnsupportedError{Functionality: "file part media type " + full}
			}
			name := "document.pdf"
			if fn := p.Filename(); fn != nil {
				name = *fn
			}
			return obj("type", "file", "file", obj("filename", name, "file_data", "data:application/pdf;base64,"+d.Data)), nil
		case "text":
			if d.Type == "url" {
				return obj("type", "text", "text", d.URL), nil
			}
			return obj("type", "text", "text", decodeText(d.Data)), nil
		}
		return nil, &UnsupportedError{Functionality: "file part media type " + mt}
	}
	return nil, nil
}

// toolArguments is serializeToolCallArguments.
func toolArguments(raw json.RawMessage) string {
	v := rawValue(raw)
	if s, ok := v.(string); ok {
		if _, err := jsonx.Parse([]byte(s)); err == nil {
			return s
		}
		return jsonx.Stringify(s)
	}
	return stringify(raw)
}

func convertOpenAIPrompt(prompt []Message) ([]any, error) {
	out := []any{}
	for _, m := range prompt {
		switch m.Role() {
		case "system":
			text, _ := m.Text()
			out = append(out, obj("role", "system", "content", text))
		case "user":
			parts := m.Parts()
			if len(parts) == 1 && parts[0].Type() == "text" {
				out = append(out, obj("role", "user", "content", parts[0].Text()))
				continue
			}
			content := []any{}
			for _, p := range parts {
				c, err := openAIContentPart(p)
				if err != nil {
					return nil, err
				}
				if c != nil {
					content = append(content, c)
				}
			}
			out = append(out, obj("role", "user", "content", content))
		case "assistant":
			var text, reasoning strings.Builder
			var calls []any
			for _, p := range m.Parts() {
				switch p.Type() {
				case "text":
					text.WriteString(p.Text())
				case "reasoning":
					reasoning.WriteString(p.Text())
				case "tool-call":
					calls = append(calls, obj("id", p.ToolCallID(), "type", "function", "function", obj("name", p.ToolName(), "arguments", toolArguments(p.Input()))))
				}
			}
			msg := jsonx.NewMap[any]()
			msg.Set("role", "assistant")
			if len(calls) > 0 && text.Len() == 0 {
				msg.Set("content", nil)
			} else {
				msg.Set("content", text.String())
			}
			if reasoning.Len() > 0 {
				msg.Set("reasoning_content", reasoning.String())
			}
			if len(calls) > 0 {
				msg.Set("tool_calls", calls)
			}
			out = append(out, msg)
		case "tool":
			for _, p := range m.Parts() {
				if p.Type() == "tool-approval-response" {
					continue
				}
				o := p.Output()
				var content string
				switch o.Type() {
				case "text", "error-text":
					content = outputValueString(o.Value())
				case "execution-denied":
					if r := o.Reason(); r != nil {
						content = *r
					} else {
						content = "Tool call execution denied."
					}
				default:
					content = stringify(o.Value())
				}
				out = append(out, obj("role", "tool", "tool_call_id", p.ToolCallID(), "content", content))
			}
		}
	}
	return out, nil
}

// OpenAIRequest is the request a call sends.
type OpenAIRequest struct {
	URL     string
	Headers map[string]string
	Body    *jsonx.Object
}

// BuildRequest is getArgs plus the stream flag and headers.
func (m *OpenAIModel) BuildRequest(opts CallOptions) (OpenAIRequest, error) {
	prompt, err := Standardize(opts.Prompt)
	if err != nil {
		return OpenAIRequest{}, err
	}
	messages, err := convertOpenAIPrompt(prompt)
	if err != nil {
		return OpenAIRequest{}, err
	}
	body := jsonx.NewMap[any]()
	body.Set("model", m.model)
	if opts.MaxOutputTokens != nil {
		body.Set("max_tokens", *opts.MaxOutputTokens)
	}
	if opts.Temperature != nil {
		body.Set("temperature", *opts.Temperature)
	}
	if opts.ResponseFormat != nil && opts.ResponseFormat.Type == "json" {
		body.Set("response_format", obj("type", "json_object"))
	}
	if opts.Reasoning != "" && opts.Reasoning != "provider-default" {
		body.Set("reasoning_effort", opts.Reasoning)
	}
	body.Set("messages", messages)
	offered := opts.offeredTools()
	if len(offered) > 0 {
		tools := make([]any, 0, len(offered))
		for _, t := range offered {
			var desc any
			if t.Description != "" {
				desc = t.Description
			}
			fn := obj("name", t.Name, "description", desc, "parameters", rawValue(t.InputSchema))
			if t.Strict != nil {
				fn.Set("strict", *t.Strict)
			}
			tools = append(tools, obj("type", "function", "function", fn))
		}
		body.Set("tools", tools)
		choice := opts.ToolChoice
		if choice == nil {
			choice = ChoiceAuto
		}
		if choice.Type == "tool" {
			body.Set("tool_choice", obj("type", "function", "function", obj("name", choice.ToolName)))
		} else {
			body.Set("tool_choice", choice.Type)
		}
	}
	body.Set("stream", true)
	headers := map[string]string{}
	if m.cfg.APIKey != "" {
		headers["authorization"] = "Bearer " + m.cfg.APIKey
	}
	for k, v := range opts.Headers {
		headers[strings.ToLower(k)] = v
	}
	return OpenAIRequest{URL: m.cfg.BaseURL + "/chat/completions", Headers: headers, Body: body}, nil
}

func (m *OpenAIModel) Stream(ctx context.Context, opts CallOptions) (Stream, error) {
	req, err := m.BuildRequest(opts)
	if err != nil {
		return nil, err
	}
	res, err := postJSON(ctx, m.cfg.Client, req.URL, req.Headers, req.Body, readErrorMessage)
	if err != nil {
		return nil, err
	}
	sse := newSSE(res.Body)
	p := &openAIParser{tracker: &toolCallTracker{used: map[string]bool{}}}
	s := &eventStream{closer: sse.close}
	s.produce = func() ([]Event, bool) {
		data, err := sse.next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				if isCanceled(ctx, err) {
					return []Event{{Type: EventError, Err: ctx.Err()}}, false
				}
				return append([]Event{{Type: EventError, Err: err}}, p.flush()...), false
			}
			return p.flush(), false
		}
		if data == "[DONE]" {
			return nil, true
		}
		return p.chunk(data), true
	}
	return s, nil
}

type openAIParser struct {
	tracker      *toolCallTracker
	pending      map[int]*pendingCall
	forwarded    map[int]bool
	finish       *FinishReason
	rawFinish    *string
	usage        json.RawMessage
	activeReason bool
	activeText   bool
	refusal      bool
	flushed      bool
}

type pendingCall struct {
	id   string
	args string
}

type openAIDelta struct {
	Index    *int    `json:"index"`
	ID       *string `json:"id"`
	Function *struct {
		Name      *string `json:"name"`
		Arguments *string `json:"arguments"`
	} `json:"function"`
}

func (p *openAIParser) chunk(data string) []Event {
	var c struct {
		Error   json.RawMessage `json:"error"`
		Usage   json.RawMessage `json:"usage"`
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
			Delta        *struct {
				Content          json.RawMessage `json:"content"`
				ReasoningContent *string         `json:"reasoning_content"`
				Reasoning        *string         `json:"reasoning"`
				Refusal          *string         `json:"refusal"`
				ToolCalls        []openAIDelta   `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		p.finish = &FinishReason{Unified: "error"}
		p.rawFinish = nil
		return []Event{{Type: EventError, Err: fmt.Errorf("Invalid JSON response: %s", err.Error())}}
	}
	if len(c.Error) > 0 && string(c.Error) != "null" {
		p.finish = &FinishReason{Unified: "error"}
		p.rawFinish = nil
		msg, _ := readErrorMessage([]byte(`{"error":` + string(c.Error) + `}`))
		return []Event{{Type: EventError, Err: &StreamError{Message: msg, Data: c.Error}}}
	}
	if len(c.Usage) > 0 && string(c.Usage) != "null" {
		p.usage = c.Usage
	}
	if len(c.Choices) == 0 {
		return nil
	}
	ch := c.Choices[0]
	if ch.FinishReason != nil {
		r := *ch.FinishReason
		p.rawFinish = &r
	}
	if ch.Delta == nil {
		return nil
	}
	var out []Event
	reasoningDelta := func(d string) {
		if p.activeText {
			out = append(out, Event{Type: EventTextEnd, ID: "txt-0"})
			p.activeText = false
		}
		if !p.activeReason {
			out = append(out, Event{Type: EventReasoningStart, ID: "reasoning-0"})
			p.activeReason = true
		}
		out = append(out, Event{Type: EventReasoningDelta, ID: "reasoning-0", Delta: d})
	}
	textDelta := func(d string) {
		if p.activeReason {
			out = append(out, Event{Type: EventReasoningEnd, ID: "reasoning-0"})
			p.activeReason = false
		}
		if !p.activeText {
			out = append(out, Event{Type: EventTextStart, ID: "txt-0"})
			p.activeText = true
		}
		out = append(out, Event{Type: EventTextDelta, ID: "txt-0", Delta: d})
	}
	d := ch.Delta
	reasoning := d.ReasoningContent
	if reasoning == nil {
		reasoning = d.Reasoning
	}
	if reasoning != nil && *reasoning != "" {
		reasoningDelta(*reasoning)
	}
	var content string
	if json.Unmarshal(d.Content, &content) == nil && content != "" {
		textDelta(content)
	}
	if d.Refusal != nil && *d.Refusal != "" {
		p.refusal = true
		textDelta(*d.Refusal)
	}
	if len(d.ToolCalls) > 0 {
		if p.activeReason {
			out = append(out, Event{Type: EventReasoningEnd, ID: "reasoning-0"})
			p.activeReason = false
		}
		for _, tc := range d.ToolCalls {
			out = append(out, p.toolDelta(tc)...)
		}
	}
	return out
}

// toolDelta buffers a call's arguments until its name arrives, then hands it
// to the tracker (processToolCallDelta).
func (p *openAIParser) toolDelta(tc openAIDelta) []Event {
	if tc.Index == nil || p.forwarded[*tc.Index] {
		return p.tracker.process(tc)
	}
	if p.pending == nil {
		p.pending = map[int]*pendingCall{}
		p.forwarded = map[int]bool{}
	}
	i := *tc.Index
	pc := p.pending[i]
	if pc == nil {
		pc = &pendingCall{}
		if tc.ID != nil {
			pc.id = *tc.ID
		}
		p.pending[i] = pc
	} else if pc.id == "" && tc.ID != nil {
		pc.id = *tc.ID
	}
	if tc.Function != nil && tc.Function.Arguments != nil {
		pc.args += *tc.Function.Arguments
	}
	if tc.Function != nil && tc.Function.Name != nil {
		args := pc.args
		id := pc.id
		fwd := openAIDelta{Index: &i}
		if id != "" {
			fwd.ID = &id
		}
		fwd.Function = &struct {
			Name      *string `json:"name"`
			Arguments *string `json:"arguments"`
		}{tc.Function.Name, &args}
		delete(p.pending, i)
		p.forwarded[i] = true
		return p.tracker.process(fwd)
	}
	return nil
}

func mapOpenAIFinish(r string, refusal bool) string {
	switch r {
	case "stop":
		if refusal {
			return "content-filter"
		}
		return "stop"
	case "length":
		return "length"
	case "content_filter":
		return "content-filter"
	case "function_call", "tool_calls":
		return "tool-calls"
	}
	return "other"
}

func (p *openAIParser) flush() []Event {
	if p.flushed {
		return nil
	}
	p.flushed = true
	var out []Event
	if p.activeReason {
		out = append(out, Event{Type: EventReasoningEnd, ID: "reasoning-0"})
	}
	if p.activeText {
		out = append(out, Event{Type: EventTextEnd, ID: "txt-0"})
	}
	idx := make([]int, 0, len(p.pending))
	for i := range p.pending {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		pc := p.pending[i]
		ii := i
		args := pc.args
		d := openAIDelta{Index: &ii, Function: &struct {
			Name      *string `json:"name"`
			Arguments *string `json:"arguments"`
		}{nil, &args}}
		if pc.id != "" {
			id := pc.id
			d.ID = &id
		}
		out = append(out, p.tracker.process(d)...)
	}
	out = append(out, p.tracker.flush()...)
	if p.rawFinish != nil {
		p.finish = &FinishReason{Unified: mapOpenAIFinish(*p.rawFinish, p.refusal), Raw: p.rawFinish}
	}
	if p.finish == nil {
		p.finish = &FinishReason{Unified: "error"}
		out = append(out, Event{Type: EventError, Err: errors.New("Response stream ended without a finish reason.")})
	}
	out = append(out, Event{Type: EventFinish, FinishReason: *p.finish, Usage: openAIUsage(p.usage)})
	return out
}

func openAIUsage(raw json.RawMessage) Usage {
	var u struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Completion *int64 `json:"completion_tokens"`
		PromptD    *struct {
			Cached *int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionD *struct {
			Reasoning *int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &u) != nil {
		return Usage{}
	}
	prompt, completion := val(u.Prompt), val(u.Completion)
	var cached, reasoning int64
	if u.PromptD != nil {
		cached = val(u.PromptD.Cached)
	}
	if u.CompletionD != nil {
		reasoning = val(u.CompletionD.Reasoning)
	}
	out := Usage{}
	out.InputTokens.Total = i64(prompt)
	out.InputTokens.NoCache = i64(prompt - cached)
	out.InputTokens.CacheRead = i64(cached)
	out.OutputTokens.Total = i64(completion)
	out.OutputTokens.Text = i64(max(0, completion-reasoning))
	out.OutputTokens.Reasoning = i64(reasoning)
	return out
}

// toolCallTracker is a reduced StreamingToolCallTracker: calls are matched by
// index (or id), started when their name arrives and finished at the end.
type toolCallTracker struct {
	calls []*trackedCall
	used  map[string]bool
}

type trackedCall struct {
	id, name, args string
	index          *int
	seq            int
	finished       bool
}

func nonBlank(s *string) string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return ""
	}
	return *s
}

func (t *toolCallTracker) process(d openAIDelta) []Event {
	var name, args *string
	if d.Function != nil {
		name, args = d.Function.Name, d.Function.Arguments
	}
	wireID, n := nonBlank(d.ID), nonBlank(name)
	var call *trackedCall
	for _, c := range t.calls {
		if c.finished {
			continue
		}
		if d.Index != nil && c.index != nil && *c.index == *d.Index && (n == "" || c.name == n) {
			call = c
		} else if d.Index == nil && wireID != "" && c.id == wireID {
			call = c
		}
	}
	if call == nil && d.Index == nil && wireID == "" && n == "" {
		var open []*trackedCall
		for _, c := range t.calls {
			if !c.finished {
				open = append(open, c)
			}
		}
		if len(open) == 1 {
			call = open[0]
		}
	}
	if call == nil {
		if n == "" {
			return nil
		}
		id := wireID
		if id == "" || t.used[id] {
			id = "call_" + secret.RandomToken(12)
		}
		t.used[id] = true
		c := &trackedCall{id: id, name: n, index: d.Index, seq: len(t.calls)}
		if args != nil {
			c.args = *args
		}
		t.calls = append(t.calls, c)
		out := []Event{{Type: EventToolInputStart, ID: id, ToolName: n}}
		if c.args != "" {
			out = append(out, Event{Type: EventToolInputDelta, ID: id, Delta: c.args})
		}
		return out
	}
	if args != nil {
		call.args += *args
		return []Event{{Type: EventToolInputDelta, ID: call.id, Delta: *args}}
	}
	return nil
}

func (t *toolCallTracker) flush() []Event {
	calls := append([]*trackedCall(nil), t.calls...)
	allIndexed := true
	for _, c := range calls {
		if c.index == nil {
			allIndexed = false
		}
	}
	if allIndexed {
		sort.SliceStable(calls, func(i, j int) bool {
			if *calls[i].index != *calls[j].index {
				return *calls[i].index < *calls[j].index
			}
			return calls[i].seq < calls[j].seq
		})
	}
	var out []Event
	for _, c := range calls {
		if c.finished {
			continue
		}
		c.finished = true
		out = append(out, Event{Type: EventToolInputEnd, ID: c.id}, Event{Type: EventToolCall, ToolCallID: c.id, ToolName: c.name, Input: c.args})
	}
	return out
}
