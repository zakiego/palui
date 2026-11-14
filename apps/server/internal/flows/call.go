package flows

// Server-side tool calls for flow steps (call.ts, RFC flows 5.1). A flow step
// resolves the tool's class from the connector's tool list before calling it,
// so a write or destructive tool is never run by accident, and passes the
// flow's chat and flow id in the tool context.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
	"github.com/zakiego/palui/apps/server/internal/tools"
)

// FlowToolError carries a message for the step log.
type FlowToolError struct{ Msg string }

func (e *FlowToolError) Error() string { return e.Msg }

const (
	StepTimeoutMs    = 20_000
	MaxStepTimeoutMs = 60_000
)

// FlowToolCaller is the connectors boundary.
type FlowToolCaller = connectors.FlowToolCaller

// WithTimeout is withTimeout: fn under a deadline, "<what> took longer than N s."
func WithTimeout[T any](ctx context.Context, ms int64, what string, fn func(ctx context.Context) (T, error)) (T, error) {
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
		return zero, &FlowToolError{fmt.Sprintf("%s took longer than %d s.", what, jsRound(float64(ms)/1000))}
	}
}

type found struct {
	info ToolInfo
	pt   connectors.PaluiTool
}

func infoOf(e connectors.RunningEntry, name string, pt connectors.PaluiTool) ToolInfo {
	approval := pt.FlowApproval
	if approval == "" {
		approval = "version"
	}
	return ToolInfo{
		Name:          tools.Namespaced(e.ID, name),
		ConnectorID:   e.ID,
		Tool:          name,
		Class:         tools.NormalizeClass(pt.Class),
		Untrusted:     pt.Untrusted || e.Row.Trust == "external",
		ConnectorType: e.Row.Type,
		FlowApproval:  approval,
		Description:   pt.Tool.Description,
		InputSchema:   pt.Tool.InputSchema,
	}
}

func toolsOf(ctx context.Context, e connectors.RunningEntry, turn connectors.TurnInfo) (set *connectors.ToolSet) {
	if e.Handle == nil || e.Handle.Tools == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			set = nil
		}
	}()
	s, err := e.Handle.Tools(ctx, turn)
	if err != nil {
		return nil
	}
	return s
}

// ConnectorFlowCaller is connectorFlowCaller over the running connectors.
type ConnectorFlowCaller struct {
	Running func() []connectors.RunningEntry
}

func NewConnectorFlowCaller(running func() []connectors.RunningEntry) *ConnectorFlowCaller {
	return &ConnectorFlowCaller{Running: running}
}

func (c *ConnectorFlowCaller) list(ctx context.Context, turn connectors.TurnInfo, only string) []found {
	var out []found
	for _, e := range c.Running() {
		if only != "" && e.ID != only {
			continue
		}
		// Another agent's own MCP server is out of reach for this flow's agent.
		if !connectors.ServesAgent(e.Row, turn.AgentID) {
			continue
		}
		set := toolsOf(ctx, e, turn)
		for _, name := range set.Keys() {
			pt, _ := set.Get(name)
			out = append(out, found{infoOf(e, name, pt), pt})
		}
	}
	return out
}

func (c *ConnectorFlowCaller) find(ctx context.Context, tool, connectorID string, turn connectors.TurnInfo) *found {
	pick := func(list []found, match func(f found) bool) *found {
		for i := range list {
			if match(list[i]) {
				return &list[i]
			}
		}
		return nil
	}
	if connectorID != "" {
		all := c.list(ctx, turn, connectorID)
		if f := pick(all, func(f found) bool { return f.info.Tool == tool }); f != nil {
			return f
		}
		return pick(all, func(f found) bool { return f.info.Name == tool })
	}
	// A namespaced name: try the connector it names first, then everything.
	if sep := strings.Index(tool, "__"); sep > 0 {
		if f := pick(c.list(ctx, turn, tool[:sep]), func(f found) bool { return f.info.Name == tool }); f != nil {
			return f
		}
	}
	return pick(c.list(ctx, turn, ""), func(f found) bool { return f.info.Name == tool })
}

func (c *ConnectorFlowCaller) List(ctx context.Context, turn connectors.TurnInfo) ([]ToolInfo, error) {
	out := []ToolInfo{}
	for _, f := range c.list(ctx, turn, "") {
		out = append(out, f.info)
	}
	return out, nil
}

func (c *ConnectorFlowCaller) Resolve(ctx context.Context, tool, connectorID string, turn connectors.TurnInfo) (*ToolInfo, error) {
	f := c.find(ctx, tool, connectorID, turn)
	if f == nil {
		return nil, nil
	}
	info := f.info
	return &info, nil
}

func (c *ConnectorFlowCaller) Call(ctx context.Context, info ToolInfo, args *jsonx.Object, turn connectors.TurnInfo, timeoutMs int64) (any, error) {
	if timeoutMs == 0 {
		timeoutMs = StepTimeoutMs
	}
	var hit *found
	for _, f := range c.list(ctx, turn, info.ConnectorID) {
		if f.info.Tool == info.Tool {
			f := f
			hit = &f
			break
		}
	}
	what := info.ConnectorID + "." + info.Tool
	if hit == nil || hit.pt.Tool.Execute == nil {
		return nil, &FlowToolError{what + " doesn't exist or is turned off."}
	}
	// The class is checked again here, right before the call: it may have changed.
	if cls := tools.NormalizeClass(hit.pt.Class); cls != info.Class {
		return nil, &FlowToolError{fmt.Sprintf("%s is now classified %s, not %s.", what, cls, info.Class)}
	}
	if info.Class == "destructive" {
		return nil, &FlowToolError{what + " is destructive; flows never run destructive tools."}
	}
	if args == nil {
		args = jsonx.NewMap[any]()
	}
	input, err := jsonx.Marshal(args)
	if err != nil {
		return nil, err
	}
	if v := hit.pt.Tool.Validate; v != nil {
		if err := v(input); err != nil {
			return nil, &FlowToolError{fmt.Sprintf("%s: the args don't match the tool's input (%s).", what, strx.Slice16(oneLine(err.Error()), 300))}
		}
	}
	exec := hit.pt.Tool.Execute
	return WithTimeout(ctx, min(timeoutMs, MaxStepTimeoutMs), what, func(ctx context.Context) (any, error) {
		return exec(ctx, input, connectors.ToolCallInfo{ToolCallID: turn.RunID})
	})
}

// StepData is stepData: MCP structuredContent, the parsed text content (or
// the text itself when it isn't JSON), or the value as is. An error result
// fails with its text.
func StepData(output any) (any, error) {
	v, err := normalizeJSON(output)
	if err != nil {
		return nil, err
	}
	if o, ok := v.(*jsonx.Object); ok {
		texts, hasTexts := contentTexts(o)
		if jsonx.Truthy(objGet(o, "isError")) {
			detail := "no detail"
			if hasTexts {
				detail = texts
			}
			return nil, &FlowToolError{"The tool returned an error: " + strx.Slice16(oneLine(detail), 300)}
		}
		if sc := objGet(o, "structuredContent"); isObjectish(sc) {
			return sc, nil
		}
		if hasTexts {
			if p, err := jsonx.Parse([]byte(texts)); err == nil {
				return p, nil
			}
			return texts, nil
		}
	}
	return v, nil
}
