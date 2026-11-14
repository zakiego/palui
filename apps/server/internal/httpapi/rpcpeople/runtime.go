package rpcpeople

import (
	"context"
	"errors"

	"github.com/zakiego/palui/apps/server/internal/httpapi"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// Runtime is the part of the in-process control API (httpapi.Runtime) these
// handlers call: what apps/app/src/server/runtime.ts's control client did.
type Runtime interface {
	WebSend(text string) (httpapi.WebSent, error)
	DecideApproval(id string, approved bool) bool
	StopChat(chatID string) bool
	SwitchAgent(chatID, agentID string) error
	AgentTools(ctx context.Context, agentID string) ([]tools.CatalogTool, error)
	Action(ctx context.Context, id, name string, input *jsonx.Object) (*jsonx.Object, error)
}

// runtimeOf is the runtime for a call; tests replace it with a fake that
// answers like the TS generator's fake control client.
var runtimeOf = func(c *httpapi.Call) (Runtime, error) {
	rt, err := c.Runtime()
	if err != nil {
		return nil, err
	}
	return rt, nil
}

// isRuntimeDown is runtime.ts isRuntimeDown: the runtime isn't there.
func isRuntimeDown(err error) bool {
	var e *httpapi.Error
	return errors.As(err, &e) && e.Code == "RUNTIME_DOWN"
}

// runtimeErrorMessage is runtime.ts runtimeErrorMessage: one sentence for the UI.
func runtimeErrorMessage(err error) string {
	if isRuntimeDown(err) {
		return httpapi.RuntimeDown
	}
	return err.Error()
}

// callRuntime is runtime.ts callRuntime: a failure keeps the runtime's
// status and becomes its message alone.
func callRuntime[T any](c *httpapi.Call, fn func(Runtime) (T, error)) (T, error) {
	var zero T
	rt, err := runtimeOf(c)
	if err != nil {
		return zero, err
	}
	out, err := fn(rt)
	if err != nil {
		return zero, rethrowRuntime(err)
	}
	return out, nil
}

func rethrowRuntime(err error) error {
	var e *httpapi.Error
	if errors.As(err, &e) {
		return &httpapi.Error{Status: e.Status, Code: e.Code, Message: runtimeErrorMessage(err)}
	}
	return errors.New(runtimeErrorMessage(err))
}

type okResult struct {
	OK bool `json:"ok"`
}
