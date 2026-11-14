// Package conntest holds the connector fakes of packages/core/src/chat/testing.ts
// (fakeChannel, modelRuntime, toolsRuntime, channelRuntime, memoryRuntime,
// fnTool, tgMessage) for the Go tests. Not imported by runtime code. The
// engine harness itself comes with the engine (M3).
package conntest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/db"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
)

// Sent is one message a FakeChannel sent.
type Sent struct {
	To      string
	Payload db.OutboxPayload
}

// FakeChannel is fakeChannel(channel): records sends, can fail the next N,
// and can be made unreachable. SendFn replaces Send when set.
type FakeChannel struct {
	mu          sync.Mutex
	channel     string
	n           int
	sent        []Sent
	FailNext    int
	Up          bool
	TypingCalls []bool
	SendFn      func(ctx context.Context, to string, payload db.OutboxPayload) (connectors.SendResult, error)
}

func NewFakeChannel(channel string) *FakeChannel { return &FakeChannel{channel: channel, Up: true} }

func (c *FakeChannel) Channel() string { return c.channel }

func (c *FakeChannel) Send(ctx context.Context, to string, payload db.OutboxPayload) (connectors.SendResult, error) {
	if c.SendFn != nil {
		return c.SendFn(ctx, to, payload)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.FailNext > 0 {
		c.FailNext--
		return connectors.SendResult{}, fmt.Errorf("%s send failed", c.channel)
	}
	c.sent = append(c.sent, Sent{to, payload})
	c.n++
	return connectors.SendResult{ChannelMsgID: fmt.Sprintf("%s-%d", c.channel, c.n)}, nil
}

func (c *FakeChannel) Typing(ctx context.Context, to string, on bool, opts connectors.TypingOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.TypingCalls = append(c.TypingCalls, on)
	return nil
}

func (c *FakeChannel) Reachable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Up
}

// SetFailNext makes the next n sends fail.
func (c *FakeChannel) SetFailNext(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.FailNext = n
}

// Sent is a copy of what was sent so far.
func (c *FakeChannel) Sent() []Sent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Sent(nil), c.sent...)
}

func manifest(id, kind, icon string) connectors.Manifest {
	return connectors.Manifest{ID: id, Kind: kind, Name: id, Icon: icon, Setup: []connectors.SetupStep{}}
}

func okTest(connectors.Context) (connectors.TestResult, error) {
	return connectors.TestResult{OK: true}, nil
}

func noStop(context.Context) error { return nil }

// ModelRuntime is modelRuntime(id, model, extra).
func ModelRuntime(id string, model func() models.LanguageModel, extra func(*connectors.RunningConnector)) *connectors.Runtime {
	return &connectors.Runtime{Manifest: manifest(id, "model", "cpu"), Test: okTest, Start: func(connectors.Context) (*connectors.RunningConnector, error) {
		h := &connectors.RunningConnector{Stop: noStop, Model: func(string, string) (models.LanguageModel, error) { return model(), nil }}
		if extra != nil {
			extra(h)
		}
		return h, nil
	}}
}

// ToolsRuntime is toolsRuntime(id, tools, kind, extra). kind "" is "tools".
func ToolsRuntime(id string, tools func() *connectors.ToolSet, kind string, extra func(*connectors.RunningConnector)) *connectors.Runtime {
	if kind == "" {
		kind = "tools"
	}
	return &connectors.Runtime{Manifest: manifest(id, kind, "wrench"), Test: okTest, Start: func(connectors.Context) (*connectors.RunningConnector, error) {
		h := &connectors.RunningConnector{Stop: noStop, Tools: func(context.Context, connectors.TurnInfo) (*connectors.ToolSet, error) { return tools(), nil }}
		if extra != nil {
			extra(h)
		}
		return h, nil
	}}
}

// ChannelRuntime is channelRuntime(id, handle).
func ChannelRuntime(id string, handle connectors.ChannelHandle) *connectors.Runtime {
	return &connectors.Runtime{Manifest: manifest(id, "channel", "send"), Test: okTest, Start: func(connectors.Context) (*connectors.RunningConnector, error) {
		return &connectors.RunningConnector{Stop: noStop, Channel: handle}, nil
	}}
}

// MemoryRuntime is memoryRuntime(handle) under the id "recall".
func MemoryRuntime(handle *connectors.MemoryHandle) *connectors.Runtime {
	return &connectors.Runtime{Manifest: manifest("recall", "memory", "brain"), Test: okTest, Start: func(connectors.Context) (*connectors.RunningConnector, error) {
		return &connectors.RunningConnector{Stop: noStop, Memory: handle}, nil
	}}
}

// FnTool is fnTool(description, fn): any input object, fn's result.
func FnTool(description string, fn func(input *jsonx.Object) (any, error)) connectors.Tool {
	return connectors.Tool{
		Description: description,
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":{}}`),
		Execute: func(ctx context.Context, input json.RawMessage, call connectors.ToolCallInfo) (any, error) {
			obj, err := jsonx.ParseObject(input)
			if err != nil {
				obj = jsonx.NewMap[any]()
			}
			return fn(obj)
		},
	}
}

// Tools builds a ToolSet from name/tool pairs, in order.
func Tools(pairs ...any) *connectors.ToolSet {
	set := connectors.NewToolSet()
	for i := 0; i+1 < len(pairs); i += 2 {
		set.Set(pairs[i].(string), pairs[i+1].(connectors.PaluiTool))
	}
	return set
}

var (
	seqMu sync.Mutex
	seq   int
)

// TgMessage is tgMessage(text, extra): a Telegram DM from the owner (42).
func TgMessage(text string) connectors.InboundMessage {
	seqMu.Lock()
	seq++
	n := seq
	seqMu.Unlock()
	return connectors.InboundMessage{Channel: "telegram", ConnectorID: "telegram", ChatExternalID: "42", ChatKind: "dm", SenderExternalID: "42", ChannelMsgID: fmt.Sprintf("m%d", n), Text: text}
}
