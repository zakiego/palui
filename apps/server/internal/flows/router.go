package flows

// The engine's flow boundaries (chat.Flows), and runFlowPrompt from
// apps/runtime/src/deps.ts.

import (
	"context"

	"github.com/zakiego/palui/apps/server/internal/chat"
	"github.com/zakiego/palui/apps/server/internal/db"
)

// FlowCommands is commands-info.ts flowCommands: the chat-trigger commands
// of the chat's enabled flows, for /help.
func FlowCommands(q db.Q, chatID string) ([]chat.FlowCommand, error) {
	list, err := ListFlows(q, chatID)
	if err != nil {
		return nil, err
	}
	out := []chat.FlowCommand{}
	for i := range list {
		f := &list[i]
		if !f.Enabled {
			continue
		}
		_, def, err := currentDef(q, f)
		if err != nil {
			// A flow without a readable version has no command to offer.
			continue
		}
		for _, t := range def.Triggers {
			if t.Kind == "chat" && t.Command != nil && *t.Command != "" {
				out = append(out, chat.FlowCommand{Command: *t.Command, Flow: f.Name})
			}
		}
	}
	return out, nil
}

// Router implements chat.Flows.
type Router struct{}

var _ chat.Flows = Router{}

func (Router) WakeRun(q db.Q, runID string, now int64) error { return WakeFlowRun(q, runID, now) }

func (Router) CheckinsPrompt(q db.Q, chatID string, now int64) (string, error) {
	return FlowCheckinsPrompt(q, chatID, now)
}

func (Router) ChatCommands(q db.Q, chatID string) ([]chat.FlowCommand, error) {
	return FlowCommands(q, chatID)
}

// RunFlowPrompt is runFlowPrompt: a prompt step as an unattended turn with
// trigger "flow" in the flow's chat, its own run id, the flow's agent and
// only the tools the step allowed.
func RunFlowPrompt(runTurn func(o chat.TurnOptions) (chat.TurnResult, error)) func(ctx context.Context, r PromptRequest) (PromptResponse, error) {
	return func(_ context.Context, r PromptRequest) (PromptResponse, error) {
		tools := append([]string{}, r.Tools...)
		send := r.Send
		res, err := runTurn(chat.TurnOptions{
			ChatID:         r.ChatID,
			Trigger:        "flow",
			Prompt:         r.Prompt,
			RunID:          r.TurnRunID,
			AgentID:        deref(r.AgentID, ""),
			ToolNames:      &tools,
			NoTools:        len(r.Tools) == 0,
			Deliver:        &send,
			SilentToken:    SilentToken,
			UntrustedInput: r.Tainted,
			ModelRole:      r.ModelRole,
		})
		if err != nil {
			return PromptResponse{}, err
		}
		return PromptResponse{Status: res.Status, Text: res.Text, RunID: res.RunID, Tainted: res.Tainted}, nil
	}
}
