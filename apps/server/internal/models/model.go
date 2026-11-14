// Package models ports packages/core/src/models (chain resolution, failover,
// prompt-cache marks, reasoning effort, metering) and replaces the AI SDK: the
// model interface, its message types, the Anthropic Messages and
// OpenAI-compatible chat completions clients, and a scripted model for tests.
package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// LanguageModel is the AI SDK's LanguageModelV4, streaming only. Stream
// returns an error before any event when the call fails up front (HTTP status,
// network, an error as the first stream chunk): that is when failover moves to
// the next link. Errors after the stream started arrive as "error" events.
type LanguageModel interface {
	// Provider is the protocol name: "anthropic.messages", "<name>.chat", ...
	Provider() string
	ModelID() string
	Stream(ctx context.Context, opts CallOptions) (Stream, error)
}

// Stream yields events in order. Recv returns io.EOF after the last one.
// Close releases the connection (safe to call more than once).
type Stream interface {
	Recv() (Event, error)
	Close() error
}

// CallOptions are LanguageModelV4CallOptions. Prompt holds ModelMessages
// (system blocks first, each with its own providerOptions for cache marks);
// providers standardize them the way streamText does.
type CallOptions struct {
	Prompt []Message
	Tools  []ToolDef
	// nil means auto when there are tools.
	ToolChoice *ToolChoice
	// Names of Tools offered; nil offers all (streamText's activeTools).
	ActiveTools     []string
	MaxOutputTokens *int
	Temperature     *float64
	// Reasoning effort ("none", "minimal", "low", "medium", "high", "xhigh",
	// "max"); "" leaves the provider's default.
	Reasoning       string
	ProviderOptions *jsonx.Object
	Headers         map[string]string
	// ResponseFormat asks for JSON (generateText's Output.object); nil is text.
	ResponseFormat *ResponseFormat
}

// ToolDef is a function tool: name, description and JSON schema.
type ToolDef struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	InputSchema     json.RawMessage `json:"inputSchema"`
	ProviderOptions *jsonx.Object   `json:"providerOptions,omitempty"`
	Strict          *bool           `json:"strict,omitempty"`
}

// ToolChoice is auto, none, required or tool (with ToolName).
type ToolChoice struct {
	Type     string `json:"type"`
	ToolName string `json:"toolName,omitempty"`
}

var (
	ChoiceAuto     = &ToolChoice{Type: "auto"}
	ChoiceNone     = &ToolChoice{Type: "none"}
	ChoiceRequired = &ToolChoice{Type: "required"}
)

// offeredTools is Tools filtered to ActiveTools, in Tools order.
func (o CallOptions) offeredTools() []ToolDef {
	if o.ActiveTools == nil {
		return o.Tools
	}
	allowed := map[string]bool{}
	for _, n := range o.ActiveTools {
		allowed[n] = true
	}
	var out []ToolDef
	for _, t := range o.Tools {
		if allowed[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// OfferedToolNames is what the model is offered on this call.
func (o CallOptions) OfferedToolNames() []string {
	var out []string
	for _, t := range o.offeredTools() {
		out = append(out, t.Name)
	}
	return out
}

// Event types (LanguageModelV4StreamPart).
const (
	EventTextStart      = "text-start"
	EventTextDelta      = "text-delta"
	EventTextEnd        = "text-end"
	EventReasoningStart = "reasoning-start"
	EventReasoningDelta = "reasoning-delta"
	EventReasoningEnd   = "reasoning-end"
	EventToolInputStart = "tool-input-start"
	EventToolInputDelta = "tool-input-delta"
	EventToolInputEnd   = "tool-input-end"
	EventToolCall       = "tool-call"
	EventFinish         = "finish"
	EventError          = "error"
)

// Event is one stream part.
type Event struct {
	Type string `json:"type"`
	// text, reasoning and tool-input parts.
	ID    string `json:"id,omitempty"`
	Delta string `json:"delta,omitempty"`
	// tool-input-start and tool-call.
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	// tool-call: the input as JSON text.
	Input            string `json:"input,omitempty"`
	ProviderExecuted bool   `json:"providerExecuted,omitempty"`
	// finish.
	FinishReason FinishReason `json:"finishReason"`
	Usage        Usage        `json:"usage"`
	// error.
	Err error `json:"-"`
	// reasoning signatures ({anthropic: {signature}}) and the like.
	ProviderMetadata *jsonx.Object `json:"providerMetadata,omitempty"`
}

// FinishReason: Unified is stop, length, content-filter, tool-calls, error,
// other or interrupted; Raw is the provider's own value.
type FinishReason struct {
	Unified string  `json:"unified"`
	Raw     *string `json:"raw,omitempty"`
}

// Usage is LanguageModelV4Usage.
type Usage struct {
	InputTokens  InputTokens  `json:"inputTokens"`
	OutputTokens OutputTokens `json:"outputTokens"`
}

type InputTokens struct {
	Total      *int64 `json:"total"`
	NoCache    *int64 `json:"noCache"`
	CacheRead  *int64 `json:"cacheRead"`
	CacheWrite *int64 `json:"cacheWrite"`
}

type OutputTokens struct {
	Total     *int64 `json:"total"`
	Text      *int64 `json:"text"`
	Reasoning *int64 `json:"reasoning"`
}

func i64(v int64) *int64 { return &v }

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// APICallError is the AI SDK's APICallError: an HTTP call that failed.
// StatusCode 0 means there was no response (network failure).
type APICallError struct {
	Message      string
	URL          string
	StatusCode   int
	ResponseBody string
	IsRetryable  bool
	Cause        error
}

func (e *APICallError) Error() string { return e.Message }
func (e *APICallError) Unwrap() error { return e.Cause }

// NewHTTPError is httpError(status) in chat/testing.ts.
func NewHTTPError(status int) *APICallError {
	return &APICallError{Message: fmt.Sprintf("HTTP %d", status), URL: "http://x", StatusCode: status}
}

// StreamError is an error part from the provider's stream (an "error" SSE
// event), with the status the provider maps it to.
type StreamError struct {
	Message     string
	Type        string
	StatusCode  int
	IsRetryable bool
	Data        json.RawMessage
}

func (e *StreamError) Error() string { return e.Message }

// --- Generate --------------------------------------------------------------------------

// ToolCall is a finished tool call.
type ToolCall struct {
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Input      string `json:"input"`
}

// GenerateResult is what a whole call produced (generateText for one step).
type GenerateResult struct {
	Text         string
	Reasoning    string
	ToolCalls    []ToolCall
	FinishReason FinishReason
	Usage        Usage
}

// Generate runs one call to the end and collects it. The first error event
// ends it with that error.
func Generate(ctx context.Context, m LanguageModel, opts CallOptions) (GenerateResult, error) {
	s, err := m.Stream(ctx, opts)
	if err != nil {
		return GenerateResult{}, err
	}
	defer s.Close()
	var r GenerateResult
	var text, reasoning strings.Builder
	for {
		ev, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return r, err
		}
		switch ev.Type {
		case EventTextDelta:
			text.WriteString(ev.Delta)
		case EventReasoningDelta:
			reasoning.WriteString(ev.Delta)
		case EventToolCall:
			r.ToolCalls = append(r.ToolCalls, ToolCall{ev.ToolCallID, ev.ToolName, ev.Input})
		case EventFinish:
			r.FinishReason, r.Usage = ev.FinishReason, ev.Usage
		case EventError:
			r.Text, r.Reasoning = text.String(), reasoning.String()
			return r, ev.Err
		}
	}
	r.Text, r.Reasoning = text.String(), reasoning.String()
	return r, nil
}

// sliceStream is a Stream over events already in hand.
type sliceStream struct {
	events []Event
	i      int
}

func (s *sliceStream) Recv() (Event, error) {
	if s.i >= len(s.events) {
		return Event{}, io.EOF
	}
	s.i++
	return s.events[s.i-1], nil
}

func (s *sliceStream) Close() error { return nil }

// NewSliceStream is a Stream over fixed events.
func NewSliceStream(events []Event) Stream { return &sliceStream{events: events} }
