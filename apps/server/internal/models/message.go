package models

import (
	"encoding/json"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Message and Part are AI SDK v7 ModelMessage JSON (the shape the TS build
// stores in messages.content, pending_approvals.messages and the context's
// system blocks). Both keep the decoded JSON object as ordered raw values, so a
// stored row reads and writes back byte for byte, unknown keys included; typed
// accessors read the fields the Go code needs.
//
// Messages: {role: "system", content: string} | {role: "user", content: string |
// Part[]} | {role: "assistant", content: string | Part[]} | {role: "tool",
// content: Part[]}, each with optional providerOptions.
//
// Parts: text {text}, image {image, mediaType?}, file {data, mediaType,
// filename?}, reasoning {text}, tool-call {toolCallId, toolName, input,
// providerExecuted?}, tool-result {toolCallId, toolName, output},
// tool-approval-request {approvalId, toolCallId}, tool-approval-response
// {approvalId, approved, reason?}; every part may carry providerOptions.

// Object is a JSON object kept as ordered raw values.
type Object struct {
	m *jsonx.Map[json.RawMessage]
}

func newObject() Object { return Object{m: jsonx.NewMap[json.RawMessage]()} }

func (o Object) Raw(k string) (json.RawMessage, bool) {
	if o.m == nil {
		return nil, false
	}
	return o.m.Get(k)
}

func (o Object) Has(k string) bool {
	_, ok := o.Raw(k)
	return ok
}

// String reads a string field ("" when missing or not a string).
func (o Object) String(k string) string {
	raw, ok := o.Raw(k)
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// StringPtr reads an optional string field.
func (o Object) StringPtr(k string) *string {
	raw, ok := o.Raw(k)
	if !ok || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

// Bool reads a boolean field (false when missing).
func (o Object) Bool(k string) bool {
	raw, ok := o.Raw(k)
	return ok && string(raw) == "true"
}

// Set writes a field, keeping its place when it already exists.
func (o *Object) Set(k string, v any) {
	if o.m == nil {
		o.m = jsonx.NewMap[json.RawMessage]()
	}
	if raw, ok := v.(json.RawMessage); ok {
		o.m.Set(k, raw)
		return
	}
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	o.m.Set(k, b)
}

func (o *Object) Delete(k string) {
	if o.m != nil {
		o.m.Delete(k)
	}
}

func (o Object) Keys() []string {
	if o.m == nil {
		return nil
	}
	return o.m.Keys()
}

// Clone is a shallow copy (raw values are immutable).
func (o Object) Clone() Object {
	if o.m == nil {
		return newObject()
	}
	return Object{m: o.m.Clone()}
}

func (o Object) MarshalJSON() ([]byte, error) { return o.m.MarshalJSON() }

func (o *Object) UnmarshalJSON(b []byte) error {
	m := jsonx.NewMap[json.RawMessage]()
	if err := m.UnmarshalJSON(b); err != nil {
		return err
	}
	o.m = m
	return nil
}

// ProviderOptions reads providerOptions as an ordered object (nil when unset).
func (o Object) ProviderOptions() *jsonx.Object {
	raw, ok := o.Raw("providerOptions")
	if !ok || string(raw) == "null" {
		return nil
	}
	obj, err := jsonx.ParseObject(raw)
	if err != nil {
		return nil
	}
	return obj
}

// SetProviderOptions writes providerOptions; nil removes it.
func (o *Object) SetProviderOptions(po *jsonx.Object) {
	if po == nil {
		o.Delete("providerOptions")
		return
	}
	o.Set("providerOptions", po)
}

// --- Message ---------------------------------------------------------------------------

type Message struct{ Object }

func (m Message) Role() string { return m.String("role") }

// Text is the content when it is a plain string.
func (m Message) Text() (string, bool) {
	raw, ok := m.Raw("content")
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// Parts is the content when it is an array (nil for string content).
func (m Message) Parts() []Part {
	raw, ok := m.Raw("content")
	if !ok || len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var parts []Part
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	return parts
}

// HasParts reports whether the content is an array.
func (m Message) HasParts() bool {
	raw, ok := m.Raw("content")
	return ok && len(raw) > 0 && raw[0] == '['
}

func (m *Message) SetParts(parts []Part) {
	if parts == nil {
		parts = []Part{}
	}
	m.Set("content", parts)
}

func (m *Message) SetText(text string) { m.Set("content", text) }

// Clone copies the message.
func (m Message) Clone() Message { return Message{m.Object.Clone()} }

func newMessage(role string) Message {
	m := Message{newObject()}
	m.Set("role", role)
	return m
}

// System is {role: "system", content}.
func System(text string) Message {
	m := newMessage("system")
	m.SetText(text)
	return m
}

// User is {role: "user", content: text}.
func User(text string) Message {
	m := newMessage("user")
	m.SetText(text)
	return m
}

// UserParts is {role: "user", content: parts}.
func UserParts(parts ...Part) Message {
	m := newMessage("user")
	m.SetParts(parts)
	return m
}

// Assistant is {role: "assistant", content: text}.
func Assistant(text string) Message {
	m := newMessage("assistant")
	m.SetText(text)
	return m
}

// AssistantParts is {role: "assistant", content: parts}.
func AssistantParts(parts ...Part) Message {
	m := newMessage("assistant")
	m.SetParts(parts)
	return m
}

// ToolMessage is {role: "tool", content: parts}.
func ToolMessage(parts ...Part) Message {
	m := newMessage("tool")
	m.SetParts(parts)
	return m
}

// WithProviderOptions returns a copy with providerOptions set.
func (m Message) WithProviderOptions(po *jsonx.Object) Message {
	c := m.Clone()
	c.SetProviderOptions(po)
	return c
}

// --- Part ------------------------------------------------------------------------------

type Part struct{ Object }

func (p Part) Type() string       { return p.String("type") }
func (p Part) Text() string       { return p.String("text") }
func (p Part) ToolCallID() string { return p.String("toolCallId") }
func (p Part) ToolName() string   { return p.String("toolName") }
func (p Part) ApprovalID() string { return p.String("approvalId") }
func (p Part) Approved() bool     { return p.Bool("approved") }
func (p Part) Reason() *string    { return p.StringPtr("reason") }
func (p Part) MediaType() string  { return p.String("mediaType") }
func (p Part) Filename() *string  { return p.StringPtr("filename") }

// ProviderExecuted is the tool call's providerExecuted flag.
func (p Part) ProviderExecuted() bool { return p.Bool("providerExecuted") }

// Input is the tool call's input JSON.
func (p Part) Input() json.RawMessage {
	raw, _ := p.Raw("input")
	return raw
}

// Output is the tool result's output object.
func (p Part) Output() ToolOutput {
	raw, ok := p.Raw("output")
	var o ToolOutput
	if ok {
		_ = json.Unmarshal(raw, &o.Object)
	}
	return o
}

// Clone copies the part.
func (p Part) Clone() Part { return Part{p.Object.Clone()} }

// WithProviderOptions returns a copy with providerOptions set.
func (p Part) WithProviderOptions(po *jsonx.Object) Part {
	c := p.Clone()
	c.SetProviderOptions(po)
	return c
}

func newPart(typ string) Part {
	p := Part{newObject()}
	p.Set("type", typ)
	return p
}

func TextPart(text string) Part {
	p := newPart("text")
	p.Set("text", text)
	return p
}

// ImagePart is {type: "image", image, mediaType?}; image is base64, a data URL or a URL.
func ImagePart(image string, mediaType string) Part {
	p := newPart("image")
	p.Set("image", image)
	if mediaType != "" {
		p.Set("mediaType", mediaType)
	}
	return p
}

// FilePart is {type: "file", data, mediaType, filename?}.
func FilePart(data string, mediaType string, filename string) Part {
	p := newPart("file")
	p.Set("data", data)
	p.Set("mediaType", mediaType)
	if filename != "" {
		p.Set("filename", filename)
	}
	return p
}

func ReasoningPart(text string) Part {
	p := newPart("reasoning")
	p.Set("text", text)
	return p
}

// ToolCallPart is {type: "tool-call", toolCallId, toolName, input}; input is
// any JSON value (json.RawMessage is written as is).
func ToolCallPart(toolCallID, toolName string, input any) Part {
	p := newPart("tool-call")
	p.Set("toolCallId", toolCallID)
	p.Set("toolName", toolName)
	p.Set("input", input)
	return p
}

func ToolResultPart(toolCallID, toolName string, output ToolOutput) Part {
	p := newPart("tool-result")
	p.Set("toolCallId", toolCallID)
	p.Set("toolName", toolName)
	p.Set("output", output.Object)
	return p
}

func ToolApprovalRequestPart(approvalID, toolCallID string) Part {
	p := newPart("tool-approval-request")
	p.Set("approvalId", approvalID)
	p.Set("toolCallId", toolCallID)
	return p
}

func ToolApprovalResponsePart(approvalID string, approved bool, reason *string) Part {
	p := newPart("tool-approval-response")
	p.Set("approvalId", approvalID)
	p.Set("approved", approved)
	if reason != nil {
		p.Set("reason", *reason)
	}
	return p
}

// ToolOutput is a ToolResultOutput: text, json, execution-denied, error-text,
// error-json or content.
type ToolOutput struct{ Object }

func (o ToolOutput) Type() string { return o.String("type") }

// Value is the raw value (a string for text and error-text).
func (o ToolOutput) Value() json.RawMessage {
	raw, _ := o.Raw("value")
	return raw
}

func (o ToolOutput) Reason() *string { return o.StringPtr("reason") }

func output(typ string) ToolOutput {
	o := ToolOutput{newObject()}
	o.Set("type", typ)
	return o
}

func OutputText(s string) ToolOutput {
	o := output("text")
	o.Set("value", s)
	return o
}

func OutputJSON(v any) ToolOutput {
	o := output("json")
	o.Set("value", v)
	return o
}

func OutputErrorText(s string) ToolOutput {
	o := output("error-text")
	o.Set("value", s)
	return o
}

func OutputErrorJSON(v any) ToolOutput {
	o := output("error-json")
	o.Set("value", v)
	return o
}

func OutputDenied(reason *string) ToolOutput {
	o := output("execution-denied")
	if reason != nil {
		o.Set("reason", *reason)
	}
	return o
}

// ParseMessages reads a stored ModelMessage array.
func ParseMessages(raw []byte) ([]Message, error) {
	var out []Message
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MarshalMessages writes messages the way JSON.stringify does.
func MarshalMessages(msgs []Message) ([]byte, error) {
	if msgs == nil {
		msgs = []Message{}
	}
	return jsonx.Marshal(msgs)
}

// OutputContent is a "content" tool output: text and file parts (what an MCP
// tool's toModelOutput gives).
func OutputContent(parts []any) ToolOutput {
	o := output("content")
	o.Set("value", parts)
	return o
}
