package chat_test

// The step loop's tool-call handling that M6 filled in: the tool's own schema
// check (the AI SDK's InvalidToolInputError, before any approval), errors as
// String(error), and a tool's toModelOutput.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/connectors/kit"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
	"github.com/zakiego/palui/apps/server/internal/zod"
)

func runOne(t *testing.T, tool connectors.Tool, input any, approval func(chat.ToolCallView) bool) []models.Message {
	t.Helper()
	tools := jsonx.NewMap[connectors.Tool]()
	tools.Set("t", tool)
	m := models.NewScripted(models.CallTool("t", input, "c1"))
	res := chat.RunLoop(context.Background(), chat.LoopOptions{
		Model: m, Messages: []models.Message{models.User("x")}, Tools: tools,
		StopWhen: func(int) bool { return true }, ToolApproval: approval, Emit: func(chat.Part) {},
	})
	return res.ResponseMessages
}

func toolResult(t *testing.T, msgs []models.Message) string {
	t.Helper()
	for _, m := range msgs {
		if m.Role() != "tool" {
			continue
		}
		for _, p := range m.Parts() {
			if p.Type() == "tool-result" {
				b, _ := json.Marshal(p.Output())
				return string(b)
			}
		}
	}
	return ""
}

func callInput(msgs []models.Message) string {
	for _, m := range msgs {
		if m.Role() != "assistant" {
			continue
		}
		for _, p := range m.Parts() {
			if p.Type() == "tool-call" {
				return string(p.Input())
			}
		}
	}
	return ""
}

var listInput = zod.Object(
	zod.F("filter", zod.Default(zod.Enum("upcoming", "done"), "upcoming")),
	zod.F("limit", zod.Default(zod.Int().Min(1).Max(50), float64(20))),
)

func TestABadToolInputIsTheAISDKsInvalidToolInputErrorAndNeverAsks(t *testing.T) {
	ran, asked := false, false
	tool := kit.Tool("list", listInput, func(context.Context, kit.Input, connectors.ToolCallInfo) (any, error) {
		ran = true
		return "ok", nil
	})
	msgs := runOne(t, tool, map[string]any{"filter": "all"}, func(chat.ToolCallView) bool { asked = true; return true })
	if ran || asked {
		t.Fatal(ran, asked)
	}
	want := `{"type":"error-text","value":"AI_InvalidToolInputError: Invalid input for tool t: AI_TypeValidationError: Type validation failed: Value: {\"filter\":\"all\"}.\nError message: [\n  {\n    \"code\": \"invalid_value\",\n    \"values\": [\n      \"upcoming\",\n      \"done\"\n    ],\n    \"path\": [\n      \"filter\"\n    ],\n    \"message\": \"Invalid option: expected one of \\\"upcoming\\\"|\\\"done\\\"\"\n  }\n]"}`
	if got := toolResult(t, msgs); got != want {
		t.Fatalf("\n got  %s\n want %s", got, want)
	}
}

func TestAGoodInputRunsWithZodsOutputAndTheCallKeepsIt(t *testing.T) {
	var seen string
	tool := kit.Tool("list", listInput, func(_ context.Context, in kit.Input, _ connectors.ToolCallInfo) (any, error) {
		seen = jsonx.Stringify(in.O)
		return kit.O("ok", true), nil
	})
	msgs := runOne(t, tool, map[string]any{"extra": 1}, nil)
	if seen != `{"filter":"upcoming","limit":20}` || callInput(msgs) != `{"filter":"upcoming","limit":20}` {
		t.Fatal(seen, callInput(msgs))
	}
	if got := toolResult(t, msgs); got != `{"type":"json","value":{"ok":true}}` {
		t.Fatal(got)
	}
}

type namedErr struct{}

func (namedErr) Error() string  { return "nope" }
func (namedErr) JSName() string { return "RecallError" }

func TestAToolErrorReachesTheModelAsStringOfTheError(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{errors.New("boom"), "Error: boom"},
		{namedErr{}, "RecallError: nope"},
	} {
		err := c.err
		tool := connectors.Tool{Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`), Execute: func(context.Context, json.RawMessage, connectors.ToolCallInfo) (any, error) {
			return nil, err
		}}
		got := toolResult(t, runOne(t, tool, map[string]any{}, nil))
		if !strings.Contains(got, `"value":"`+c.want+`"`) {
			t.Fatal(got)
		}
	}
}

func TestAnUnknownToolIsTheAISDKsNoSuchToolError(t *testing.T) {
	tools := jsonx.NewMap[connectors.Tool]()
	tools.Set("t", connectors.Tool{Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)})
	m := models.NewScripted(models.CallTool("other", map[string]any{}, "c1"))
	res := chat.RunLoop(context.Background(), chat.LoopOptions{Model: m, Messages: []models.Message{models.User("x")}, Tools: tools, StopWhen: func(int) bool { return true }, Emit: func(chat.Part) {}})
	if got := toolResult(t, res.ResponseMessages); !strings.Contains(got, `AI_NoSuchToolError: Model tried to call unavailable tool 'other'. Available tools: t.`) {
		t.Fatal(got)
	}
}

func TestAToolsToModelOutputShapesWhatTheModelSees(t *testing.T) {
	tool := connectors.Tool{
		Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`),
		Execute: func(context.Context, json.RawMessage, connectors.ToolCallInfo) (any, error) {
			return kit.O("content", []any{kit.O("type", "text", "text", "hi")}), nil
		},
		ToModelOutput: func(any) models.ToolOutput {
			return models.OutputContent([]any{kit.O("type", "text", "text", "hi")})
		},
	}
	if got := toolResult(t, runOne(t, tool, map[string]any{}, nil)); got != `{"type":"content","value":[{"type":"text","text":"hi"}]}` {
		t.Fatal(got)
	}
}
