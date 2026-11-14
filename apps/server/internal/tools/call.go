package tools

// Calls a connector tool server-side, outside any turn, from tools/call.ts:
// flow schedule sources and polls read their data on a timer, so they must not
// depend on which tools a turn happened to collect. Only `read` tools are
// allowed: nobody approves these calls.

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// ToolCallError carries a message for the owner.
type ToolCallError struct{ Msg string }

func (e *ToolCallError) Error() string { return e.Msg }

const ToolCallTimeoutMs = 20_000

// CallScope is the part of a turn a source call carries (agent and memory scope).
type CallScope struct {
	AgentID string
	Memory  *connectors.MemoryScope
}

// ToolCaller is the flows/sources ToolCaller type (M5 uses it): call one tool
// of a connector by bare name.
type ToolCaller func(ctx context.Context, connectorID, tool string, args *jsonx.Object, scope *CallScope) (any, error)

type CallerOptions struct {
	TZ        func() string
	TimeoutMs int64
	Clock     func() int64
}

func withTimeout[T any](ctx context.Context, ms int64, what string, fn func(ctx context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	defer cancel()
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				var zero T
				ch <- res{zero, fmt.Errorf("%v", r)}
			}
		}()
		v, err := fn(ctx)
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, &ToolCallError{fmt.Sprintf("%s took longer than %d s.", what, int64(math.Floor(float64(ms)/1000+0.5)))}
	}
}

// ConnectorToolCaller is connectorToolCaller.
func ConnectorToolCaller(running func() []connectors.RunningEntry, o CallerOptions) ToolCaller {
	if o.TZ == nil {
		o.TZ = func() string { return "UTC" }
	}
	if o.Clock == nil {
		o.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	return func(ctx context.Context, connectorID, toolName string, args *jsonx.Object, scope *CallScope) (any, error) {
		var entry *connectors.RunningEntry
		for _, e := range running() {
			if e.ID == connectorID {
				e := e
				entry = &e
				break
			}
		}
		if entry == nil {
			return nil, &ToolCallError{fmt.Sprintf("Connector %s isn't running. Turn it on in Connectors.", connectorID)}
		}
		what := connectorID + "." + toolName
		timeout := o.TimeoutMs
		if timeout == 0 {
			timeout = ToolCallTimeoutMs
		}
		ensureRead := func(cls string) error {
			if c := NormalizeClass(cls); c != "read" {
				return &ToolCallError{fmt.Sprintf("%s is classified %s; only read tools can feed a flow source. Mark it read on the Connectors page if it only reads.", what, c)}
			}
			return nil
		}
		if args == nil {
			args = jsonx.NewMap[any]()
		}
		if entry.Handle.CallTool != nil {
			return withTimeout(ctx, timeout, what, func(ctx context.Context) (any, error) {
				r, err := entry.Handle.CallTool(ctx, toolName, args)
				if err != nil {
					return nil, err
				}
				if err := ensureRead(r.Class); err != nil {
					return nil, err
				}
				return r.Output, nil
			})
		}
		if entry.Handle.Tools == nil {
			return nil, &ToolCallError{fmt.Sprintf("Connector %s has no tools.", connectorID)}
		}
		now := o.Clock()
		turn := connectors.TurnInfo{RunID: fmt.Sprintf("source_%d", now), Trigger: "schedule", TZ: o.TZ(), Now: now}
		if scope != nil {
			turn.AgentID, turn.Memory = scope.AgentID, scope.Memory
		}
		set, err := entry.Handle.Tools(ctx, turn)
		if err != nil {
			return nil, err
		}
		var pt connectors.PaluiTool
		ok := false
		if set != nil {
			pt, ok = set.Get(toolName)
		}
		if !ok || pt.Tool.Execute == nil {
			return nil, &ToolCallError{fmt.Sprintf("%s doesn't exist or is turned off.", what)}
		}
		if err := ensureRead(pt.Class); err != nil {
			return nil, err
		}
		input, _ := jsonx.Marshal(args)
		return withTimeout(ctx, timeout, what, func(ctx context.Context) (any, error) {
			return pt.Tool.Execute(ctx, input, connectors.ToolCallInfo{ToolCallID: fmt.Sprintf("source_%d", now)})
		})
	}
}
