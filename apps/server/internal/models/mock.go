package models

import (
	"context"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/secret"
)

// The scripted model for tests: chat/testing.ts's scriptedModel over
// MockLanguageModelV4. Each call plays the next step; when the steps run out
// it plays the fallback. It keeps the prompts, offered tool names and tool
// choices it saw. Not used by runtime code.

// Step is one call's script: Events to stream, Err to fail the call with, or
// Fn to compute either when the call happens.
type Step struct {
	Events []Event
	Err    error
	Fn     func(ctx context.Context, opts CallOptions) ([]Event, error)
}

// ScriptedModel implements LanguageModel.
type ScriptedModel struct {
	mu       sync.Mutex
	steps    []Step
	fallback Step
	provider string
	// Prompts seen, in call order.
	Prompts [][]Message
	// Tool names offered on each call.
	Offered [][]string
	// Tool choice type of each call ("" when unset).
	Choices []string
	// Full options of each call.
	Calls []CallOptions
}

// NewScripted is scriptedModel(steps, fallback = say("ok")).
func NewScripted(steps ...Step) *ScriptedModel {
	return &ScriptedModel{steps: steps, fallback: Say("ok"), provider: "mock-provider"}
}

// WithFallback sets the step played when the script runs out.
func (m *ScriptedModel) WithFallback(s Step) *ScriptedModel {
	m.fallback = s
	return m
}

// WithProvider names the protocol the way a real one does ("anthropic.messages").
func (m *ScriptedModel) WithProvider(p string) *ScriptedModel {
	m.provider = p
	return m
}

// Push appends steps.
func (m *ScriptedModel) Push(steps ...Step) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, steps...)
}

// Unshift puts steps before the remaining ones (steps.unshift).
func (m *ScriptedModel) Unshift(steps ...Step) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(append([]Step{}, steps...), m.steps...)
}

// PromptsSeen copies Prompts under the lock (a model call runs in its own
// goroutine, so a test reading Prompts directly races with it).
func (m *ScriptedModel) PromptsSeen() [][]Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]Message(nil), m.Prompts...)
}

// Remaining is the number of unplayed steps.
func (m *ScriptedModel) Remaining() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.steps)
}

func (m *ScriptedModel) Provider() string { return m.provider }
func (m *ScriptedModel) ModelID() string  { return "mock-model-id" }

func (m *ScriptedModel) Stream(ctx context.Context, opts CallOptions) (Stream, error) {
	m.mu.Lock()
	m.Prompts = append(m.Prompts, opts.Prompt)
	m.Offered = append(m.Offered, opts.OfferedToolNames())
	choice := ""
	if opts.ToolChoice != nil {
		choice = opts.ToolChoice.Type
	} else if len(opts.offeredTools()) > 0 {
		choice = "auto"
	}
	m.Choices = append(m.Choices, choice)
	m.Calls = append(m.Calls, opts)
	s := m.fallback
	if len(m.steps) > 0 {
		s = m.steps[0]
		m.steps = m.steps[1:]
	}
	m.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	events := s.Events
	if s.Fn != nil {
		var err error
		if events, err = s.Fn(ctx, opts); err != nil {
			return nil, err
		}
	}
	return NewSliceStream(events), nil
}

// MockUsage is the usage every scripted step reports: n input, 5 output.
func MockUsage(n int64) Usage {
	return Usage{InputTokens: InputTokens{Total: i64(n), NoCache: i64(n)}, OutputTokens: OutputTokens{Total: i64(5), Text: i64(5)}}
}

func finishEvent(reason string) Event {
	return Event{Type: EventFinish, FinishReason: FinishReason{Unified: reason}, Usage: MockUsage(10)}
}

// Say is a step answering text.
func Say(text string) Step {
	return Step{Events: []Event{
		{Type: EventTextStart, ID: "t"},
		{Type: EventTextDelta, ID: "t", Delta: text},
		{Type: EventTextEnd, ID: "t"},
		finishEvent("stop"),
	}}
}

// CallTool is a step calling one tool; an empty id is made up.
func CallTool(toolName string, input any, toolCallID string) Step {
	if toolCallID == "" {
		toolCallID = "call-" + secret.RandomToken(4)
	}
	return Step{Events: []Event{
		{Type: EventToolCall, ToolCallID: toolCallID, ToolName: toolName, Input: jsonx.Stringify(input)},
		finishEvent("tool-calls"),
	}}
}

// ScriptedCall is one call of CallTools.
type ScriptedCall struct {
	ToolName   string
	Input      any
	ToolCallID string
}

// CallTools is several tool calls in one step, in this order.
func CallTools(calls ...ScriptedCall) Step {
	var evs []Event
	for _, c := range calls {
		evs = append(evs, Event{Type: EventToolCall, ToolCallID: c.ToolCallID, ToolName: c.ToolName, Input: jsonx.Stringify(c.Input)})
	}
	return Step{Events: append(evs, finishEvent("tool-calls"))}
}

// Fail is a step whose call fails with err (httpError(status) is NewHTTPError).
func Fail(err error) Step { return Step{Err: err} }

// PromptText is promptText: the prompt as JSON, for assertions.
func PromptText(prompt []Message) string {
	b, _ := MarshalMessages(prompt)
	return string(b)
}
