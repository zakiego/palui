package chat

// The tool loop the TS turn got from the AI SDK's streamText (ai 7.0.166):
// steps, tool execution, tool approval requests, approval responses on resume,
// stop conditions, retries, and the fullStream parts the turn folds over. Only
// what turn.ts uses is ported; the observable behavior (part order, the
// response messages stored with an approval, finish reasons, error texts) is
// streamText's.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/zakiego/palui/apps/server/internal/connectors"
	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/models"
)

// Part is one fullStream part. Type is start-step, text-delta, finish-step,
// tool-call, tool-approval-request, tool-result, tool-error,
// tool-output-denied, error, abort or finish.
type Part struct {
	Type string
	// text-delta.
	Text string
	// finish-step and finish: stop, length, content-filter, tool-calls, error, other.
	FinishReason string
	// tool parts.
	ToolCallID string
	ToolName   string
	// The parsed input (a JSON value: *jsonx.Object, []any, string, float64,
	// bool or nil) and its JSON text.
	Input     any
	InputJSON string
	// tool-result: the tool's output; OutputUndefined when it returned nothing.
	Output          any
	OutputUndefined bool
	// tool-error and error.
	Err error
	// tool-approval-request.
	ApprovalID string
	// finish: the turn's total usage.
	Usage TotalUsage
}

// TotalUsage is the summed LanguageModelUsage of every step.
type TotalUsage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// StepPrep is what prepareStep returns. A nil field keeps the default.
type StepPrep struct {
	ToolChoice  *models.ToolChoice
	Messages    []models.Message
	ActiveTools []string
}

// ToolCallView is the call toolApproval sees.
type ToolCallView struct {
	ToolCallID string
	ToolName   string
	Input      any
	InputJSON  string
}

// LoopOptions are streamText's options as turn.ts sets them.
type LoopOptions struct {
	Model    models.LanguageModel
	System   []models.Message
	Messages []models.Message
	// Every tool the turn may run, in order.
	Tools *jsonx.Map[connectors.Tool]
	// The names offered when PrepareStep gives none; nil offers all.
	ActiveTools []string
	PrepareStep func(stepNumber int, messages []models.Message) StepPrep
	MaxRetries  int
	// StopWhen is checked after every step that ran tools (isStepCount, draining).
	StopWhen func(steps int) bool
	// ToolApproval says whether a call waits for the owner ("user-approval").
	ToolApproval func(call ToolCallView) bool
	// Emit gets every part, in order, in the caller's goroutine.
	Emit func(p Part)
	// RetryDelay is the first retry wait (2 s, doubling); tests shorten it.
	RetryDelay time.Duration
}

// LoopResult is what the loop leaves behind.
type LoopResult struct {
	// responseMessages: the tool message of a resume, then each step's messages.
	ResponseMessages []models.Message
	// False when the loop ended without a finished step.
	Finished bool
}

var errAborted = errors.New("aborted")

// ToolChoiceViolationError is the AI SDK's AI_ToolChoiceViolationError.
type ToolChoiceViolationError struct{ Message string }

func (e *ToolChoiceViolationError) Error() string { return e.Message }

// RetryError is the AI SDK's RetryError.
type RetryError struct {
	Message string
	Errors  []error
}

func (e *RetryError) Error() string { return e.Message }
func (e *RetryError) Unwrap() error {
	if len(e.Errors) == 0 {
		return nil
	}
	return e.Errors[len(e.Errors)-1]
}

// NoOutputGeneratedError is the AI SDK's NoOutputGeneratedError.
type NoOutputGeneratedError struct{ Message string }

func (e *NoOutputGeneratedError) Error() string { return e.Message }

// JSNamed is an error with a JS error name (the AI SDK's AI_* errors). Any
// other Go error is a plain JS Error.
type JSNamed interface{ JSName() string }

// ErrorString is String(error) / error.toString(): "<name>: <message>". The
// AI SDK's getErrorMessage uses it for a tool error the model sees.
func ErrorString(err error) string {
	if err == nil {
		return "unknown error"
	}
	name := "Error"
	var n JSNamed
	if errors.As(err, &n) {
		name = n.JSName()
	}
	return name + ": " + err.Error()
}

// NoSuchToolError is the AI SDK's NoSuchToolError.
type NoSuchToolError struct{ Message string }

func (e *NoSuchToolError) Error() string  { return e.Message }
func (e *NoSuchToolError) JSName() string { return "AI_NoSuchToolError" }

// InvalidToolInputError is the AI SDK's InvalidToolInputError.
type InvalidToolInputError struct {
	ToolName string
	Cause    error
}

func (e *InvalidToolInputError) Error() string {
	return "Invalid input for tool " + e.ToolName + ": " + ErrorString(e.Cause)
}
func (e *InvalidToolInputError) JSName() string { return "AI_InvalidToolInputError" }
func (e *InvalidToolInputError) Unwrap() error  { return e.Cause }

// JSONParseError is the AI SDK's JSONParseError for a tool call's input.
type JSONParseError struct {
	Text  string
	Cause error
}

func (e *JSONParseError) Error() string {
	return fmt.Sprintf("JSON parsing failed: Text: %s.\nError message: SyntaxError: %s", e.Text, e.Cause.Error())
}
func (e *JSONParseError) JSName() string { return "AI_JSONParseError" }

// ErrorMessage is getErrorMessage: an error's message, a string as is.
func ErrorMessage(err any) string {
	switch e := err.(type) {
	case nil:
		return "unknown error"
	case string:
		return e
	case error:
		return e.Error()
	}
	return jsonx.Stringify(err)
}

const idAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// generateID is the AI SDK's generateId: 16 alphanumeric characters.
func generateID() string {
	var b strings.Builder
	for i := 0; i < 16; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(idAlphabet))))
		if err != nil {
			panic(err)
		}
		b.WriteByte(idAlphabet[n.Int64()])
	}
	return b.String()
}

type loop struct {
	ctx     context.Context
	opts    LoopOptions
	aborted bool
	usage   TotalUsage
}

// emit sends p unless the turn was aborted; the first part after an abort is
// replaced by the abort part, and the loop unwinds.
func (l *loop) emit(p Part) error {
	if l.aborted {
		return errAborted
	}
	if l.ctx.Err() != nil {
		l.aborted = true
		l.opts.Emit(Part{Type: "abort"})
		return errAborted
	}
	l.opts.Emit(p)
	return nil
}

// RunLoop runs the step loop until a step ends without tool calls, a stop
// condition holds, a call waits for approval, the model fails or ctx is done.
func RunLoop(ctx context.Context, opts LoopOptions) LoopResult {
	l := &loop{ctx: ctx, opts: opts}
	if opts.RetryDelay == 0 {
		l.opts.RetryDelay = 2 * time.Second
	}
	var res LoopResult
	_ = l.run(&res)
	if l.ctx.Err() != nil && !l.aborted {
		l.aborted = true
		opts.Emit(Part{Type: "abort"})
	}
	return res
}

// --- content of a step (toResponseMessages input) -------------------------------------

type contentItem struct {
	kind string // text, reasoning, tool-call, tool-approval-request, tool-result, tool-error
	text *strings.Builder
	po   *jsonx.Object
	call *toolCall
	// tool-approval-request.
	approvalID string
	// tool-result / tool-error.
	output          any
	outputUndefined bool
	err             error
	// The tool's toModelOutput (MCP tools).
	toModel func(any) models.ToolOutput
}

type toolCall struct {
	id      string
	name    string
	input   any
	json    string
	invalid bool
	err     error
}

func (l *loop) run(res *LoopResult) error {
	messages := l.opts.Messages
	var initialResponse []models.Message
	if len(messages) > 0 && messages[len(messages)-1].Role() == "tool" {
		msg, err := l.resumeApprovals(messages)
		if err != nil {
			return err
		}
		if msg != nil {
			initialResponse = append(initialResponse, *msg)
		}
	}
	res.ResponseMessages = append(res.ResponseMessages, initialResponse...)
	stepInput := append(append([]models.Message{}, messages...), initialResponse...)
	steps := 0
	for step := 0; ; step++ {
		var prep StepPrep
		if l.opts.PrepareStep != nil {
			prep = l.opts.PrepareStep(step, stepInput)
		}
		active := l.opts.ActiveTools
		if prep.ActiveTools != nil {
			active = prep.ActiveTools
		}
		stepTools := filterTools(l.opts.Tools, active)
		choice := prep.ToolChoice
		if choice == nil {
			choice = models.ChoiceAuto
		}
		stepMessages := stepInput
		if prep.Messages != nil {
			stepMessages = prep.Messages
		}
		out, err := l.step(stepTools, choice, stepMessages)
		if err != nil {
			return err
		}
		if !out.finished {
			return nil
		}
		steps++
		res.ResponseMessages = append(res.ResponseMessages, out.response...)
		res.Finished = true
		next := out.clientCalls > 0 && out.clientCalls == out.outputs && out.finishReason != "interrupted"
		if next && l.opts.StopWhen != nil && l.opts.StopWhen(steps) {
			next = false
		}
		if !next {
			return l.emit(Part{Type: "finish", FinishReason: out.finishReason, Usage: l.usage})
		}
		stepInput = append(append([]models.Message{}, stepMessages...), out.response...)
	}
}

func filterTools(all *jsonx.Map[connectors.Tool], active []string) *jsonx.Map[connectors.Tool] {
	if all == nil {
		return jsonx.NewMap[connectors.Tool]()
	}
	if active == nil {
		return all
	}
	allowed := map[string]bool{}
	for _, n := range active {
		allowed[n] = true
	}
	out := jsonx.NewMap[connectors.Tool]()
	for _, k := range all.Keys() {
		if allowed[k] {
			t, _ := all.Get(k)
			out.Set(k, t)
		}
	}
	return out
}

func toolDefs(tools *jsonx.Map[connectors.Tool]) []models.ToolDef {
	var defs []models.ToolDef
	for _, k := range tools.Keys() {
		t, _ := tools.Get(k)
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		defs = append(defs, models.ToolDef{Name: k, Description: t.Description, InputSchema: schema, Strict: t.Strict})
	}
	return defs
}

type stepOut struct {
	finished     bool
	finishReason string
	clientCalls  int
	outputs      int
	response     []models.Message
}

// callModel is callLanguageModel with streamText's retry (maxRetries,
// exponential backoff from RetryDelay, retryable APICallErrors only).
func (l *loop) callModel(opts models.CallOptions) (models.Stream, error) {
	var errs []error
	delay := l.opts.RetryDelay
	for {
		s, err := l.opts.Model.Stream(l.ctx, opts)
		if err == nil {
			return s, nil
		}
		if l.ctx.Err() != nil {
			return nil, err
		}
		if l.opts.MaxRetries == 0 {
			return nil, err
		}
		errs = append(errs, err)
		try := len(errs)
		if try > l.opts.MaxRetries {
			return nil, &RetryError{Message: fmt.Sprintf("Failed after %d attempts. Last error: %s", try, ErrorMessage(err)), Errors: errs}
		}
		var api *models.APICallError
		if !errors.As(err, &api) || !api.IsRetryable {
			if try == 1 {
				return nil, err
			}
			return nil, &RetryError{Message: fmt.Sprintf("Failed after %d attempts with non-retryable error: '%s'", try, ErrorMessage(err)), Errors: errs}
		}
		select {
		case <-time.After(delay):
		case <-l.ctx.Done():
			return nil, l.ctx.Err()
		}
		delay *= 2
	}
}

type streamItem struct {
	ev  models.Event
	err error
}

// pump reads the model stream in its own goroutine so an abort is seen at
// once, even while the model (or a scripted test step) blocks.
func (l *loop) pump(opts models.CallOptions) (<-chan streamItem, func()) {
	ch := make(chan streamItem, 16)
	var mu sync.Mutex
	var stream models.Stream
	closed := false
	go func() {
		defer close(ch)
		s, err := l.callModel(opts)
		if err != nil {
			select {
			case ch <- streamItem{err: err}:
			case <-l.ctx.Done():
			}
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			_ = s.Close()
			return
		}
		stream = s
		mu.Unlock()
		for {
			ev, err := s.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			select {
			case ch <- streamItem{ev: ev, err: err}:
			case <-l.ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	stop := func() {
		mu.Lock()
		closed = true
		s := stream
		mu.Unlock()
		if s != nil {
			_ = s.Close()
		}
	}
	return ch, stop
}

func (l *loop) step(tools *jsonx.Map[connectors.Tool], choice *models.ToolChoice, messages []models.Message) (stepOut, error) {
	var out stepOut
	prompt := append(append([]models.Message{}, l.opts.System...), messages...)
	callOpts := models.CallOptions{Prompt: prompt, Tools: toolDefs(tools), ToolChoice: choice}
	withhold := choice.Type == "none"
	ch, stop := l.pump(callOpts)
	defer stop()

	started := false
	startStep := func() error {
		if started {
			return nil
		}
		started = true
		return l.emit(Part{Type: "start-step"})
	}
	var content []*contentItem
	texts := map[string]*contentItem{}
	reasonings := map[string]*contentItem{}
	var calls []*toolCall
	var toExecute []*toolCall
	withheld := 0
	finishReason := "other"
	terminal, gotOutput := false, false
	var stepUsage models.Usage

	for {
		var item streamItem
		var ok bool
		select {
		case item, ok = <-ch:
		case <-l.ctx.Done():
			return out, l.emit(Part{})
		}
		if !ok {
			break
		}
		if item.err != nil {
			if !started {
				// The call failed up front: streamText's top-level catch.
				return out, l.emit(Part{Type: "error", Err: item.err})
			}
			if err := l.emit(Part{Type: "error", Err: item.err}); err != nil {
				return out, err
			}
			terminal = true
			finishReason = "error"
			break
		}
		ev := item.ev
		if err := startStep(); err != nil {
			return out, err
		}
		switch ev.Type {
		case models.EventTextStart:
			c := &contentItem{kind: "text", text: &strings.Builder{}, po: ev.ProviderMetadata}
			texts[ev.ID] = c
			content = append(content, c)
		case models.EventTextDelta:
			c := texts[ev.ID]
			if c == nil {
				if err := l.emit(Part{Type: "error", Err: fmt.Errorf("text part %s not found", ev.ID)}); err != nil {
					return out, err
				}
				continue
			}
			c.text.WriteString(ev.Delta)
			if ev.ProviderMetadata != nil {
				c.po = ev.ProviderMetadata
			}
			if ev.Delta != "" {
				gotOutput = true
				if err := l.emit(Part{Type: "text-delta", Text: ev.Delta}); err != nil {
					return out, err
				}
			}
		case models.EventTextEnd:
			if c := texts[ev.ID]; c != nil {
				if ev.ProviderMetadata != nil {
					c.po = ev.ProviderMetadata
				}
				delete(texts, ev.ID)
			}
		case models.EventReasoningStart:
			c := &contentItem{kind: "reasoning", text: &strings.Builder{}, po: ev.ProviderMetadata}
			reasonings[ev.ID] = c
			content = append(content, c)
		case models.EventReasoningDelta:
			if c := reasonings[ev.ID]; c != nil {
				c.text.WriteString(ev.Delta)
				if ev.ProviderMetadata != nil {
					c.po = ev.ProviderMetadata
				}
				if ev.Delta != "" {
					gotOutput = true
				}
			}
		case models.EventReasoningEnd:
			if c := reasonings[ev.ID]; c != nil {
				if ev.ProviderMetadata != nil {
					c.po = ev.ProviderMetadata
				}
				delete(reasonings, ev.ID)
			}
		case models.EventToolInputDelta:
			if ev.Delta != "" {
				gotOutput = true
			}
		case models.EventToolCall:
			call := parseToolCall(ev, tools)
			if withhold {
				withheld++
				continue
			}
			gotOutput = true
			calls = append(calls, call)
			content = append(content, &contentItem{kind: "tool-call", call: call})
			if err := l.emit(Part{Type: "tool-call", ToolCallID: call.id, ToolName: call.name, Input: call.input, InputJSON: call.json}); err != nil {
				return out, err
			}
			if call.invalid {
				content = append(content, &contentItem{kind: "tool-error", call: call, err: call.err})
				out.outputs++
				if err := l.emit(Part{Type: "tool-error", ToolCallID: call.id, ToolName: call.name, Input: call.input, InputJSON: call.json, Err: call.err}); err != nil {
					return out, err
				}
				continue
			}
			tool, _ := tools.Get(call.name)
			if l.opts.ToolApproval != nil && l.opts.ToolApproval(ToolCallView{ToolCallID: call.id, ToolName: call.name, Input: call.input, InputJSON: call.json}) {
				id := generateID()
				content = append(content, &contentItem{kind: "tool-approval-request", call: call, approvalID: id})
				if err := l.emit(Part{Type: "tool-approval-request", ApprovalID: id, ToolCallID: call.id, ToolName: call.name, Input: call.input, InputJSON: call.json}); err != nil {
					return out, err
				}
				continue
			}
			if tool.Execute != nil {
				toExecute = append(toExecute, call)
			}
		case models.EventFinish:
			terminal = true
			stepUsage = ev.Usage
			finishReason = ev.FinishReason.Unified
			if withhold && withheld > 0 {
				finishReason = "error"
				if err := l.emit(Part{Type: "error", Err: &ToolChoiceViolationError{Message: "Model response contained a tool call even though tool choice was none."}}); err != nil {
					return out, err
				}
				continue
			}
			if finishReason == "stop" || finishReason == "tool-calls" {
				results := l.execute(toExecute, messages)
				for _, r := range results {
					if r == nil {
						// Aborted while the tools ran.
						return out, l.emit(Part{})
					}
					content = append(content, r)
					out.outputs++
					p := Part{ToolCallID: r.call.id, ToolName: r.call.name, Input: r.call.input, InputJSON: r.call.json}
					if r.kind == "tool-error" {
						p.Type, p.Err = "tool-error", r.err
					} else {
						p.Type, p.Output, p.OutputUndefined = "tool-result", r.output, r.outputUndefined
					}
					if err := l.emit(p); err != nil {
						return out, err
					}
				}
			}
		case models.EventError:
			terminal = true
			finishReason = "error"
			if err := l.emit(Part{Type: "error", Err: ev.Err}); err != nil {
				return out, err
			}
		}
	}
	if !terminal && !gotOutput {
		if err := startStep(); err != nil {
			return out, err
		}
		return out, l.emit(Part{Type: "error", Err: &NoOutputGeneratedError{Message: "No output generated. The model stream ended without a finish chunk."}})
	}
	if err := l.emit(Part{Type: "finish-step", FinishReason: finishReason}); err != nil {
		return out, err
	}
	l.addUsage(stepUsage)
	out.finished = true
	out.finishReason = finishReason
	out.clientCalls = len(calls)
	out.response = toResponseMessages(content)
	return out, nil
}

func (l *loop) addUsage(u models.Usage) {
	v := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	l.usage.InputTokens += v(u.InputTokens.Total)
	l.usage.OutputTokens += v(u.OutputTokens.Total)
	l.usage.CacheReadTokens += v(u.InputTokens.CacheRead)
	l.usage.CacheWriteTokens += v(u.InputTokens.CacheWrite)
}

// parseToolCall is parseToolCall for function tools: the input must be JSON
// (an empty input is {}) and the tool must be offered this step. A call that
// fails either is kept as an invalid call.
func parseToolCall(ev models.Event, tools *jsonx.Map[connectors.Tool]) *toolCall {
	c := &toolCall{id: ev.ToolCallID, name: ev.ToolName}
	text := ev.Input
	if strings.TrimSpace(text) == "" {
		text = "{}"
	}
	v, perr := jsonx.Parse([]byte(text))
	if perr == nil {
		c.input, c.json = v, jsonx.Stringify(v)
	} else {
		c.input, c.json = ev.Input, jsonx.Stringify(ev.Input)
	}
	tool, ok := tools.Get(ev.ToolName)
	if !ok {
		c.invalid = true
		c.err = &NoSuchToolError{fmt.Sprintf("Model tried to call unavailable tool '%s'. Available tools: %s.", ev.ToolName, strings.Join(tools.Keys(), ", "))}
		if tools.Len() == 0 {
			c.err = &NoSuchToolError{fmt.Sprintf("Model tried to call unavailable tool '%s'. No tools are available.", ev.ToolName)}
		}
		return c
	}
	if perr != nil {
		c.invalid = true
		c.err = &InvalidToolInputError{ToolName: ev.ToolName, Cause: &JSONParseError{Text: ev.Input, Cause: perr}}
		return c
	}
	// The tool's own schema (zod in TS): the call keeps zod's output.
	if tool.Parse != nil {
		parsed, err := tool.Parse(v)
		if err != nil {
			c.invalid = true
			c.err = &InvalidToolInputError{ToolName: ev.ToolName, Cause: err}
			return c
		}
		c.input, c.json = parsed, jsonx.Stringify(parsed)
	}
	return c
}

// execute runs a step's tools at once and returns their results in call
// order; a nil entry means the turn was aborted first.
func (l *loop) execute(calls []*toolCall, messages []models.Message) []*contentItem {
	results := make([]*contentItem, len(calls))
	done := make([]chan struct{}, len(calls))
	for i, c := range calls {
		done[i] = make(chan struct{})
		tool, _ := l.opts.Tools.Get(c.name)
		go func(i int, c *toolCall, tool connectors.Tool) {
			defer close(done[i])
			results[i] = runTool(l.ctx, tool, c, messages)
		}(i, c, tool)
	}
	out := make([]*contentItem, len(calls))
	for i := range calls {
		select {
		case <-done[i]:
			out[i] = results[i]
		case <-l.ctx.Done():
			return out[:i+1]
		}
	}
	return out
}

func runTool(ctx context.Context, tool connectors.Tool, c *toolCall, messages []models.Message) (item *contentItem) {
	defer func() {
		if r := recover(); r != nil {
			item = &contentItem{kind: "tool-error", call: c, err: fmt.Errorf("%v", r)}
		}
	}()
	raw := json.RawMessage(c.json)
	v, err := tool.Execute(ctx, raw, connectors.ToolCallInfo{ToolCallID: c.id, Messages: messages})
	if err != nil {
		return &contentItem{kind: "tool-error", call: c, err: err}
	}
	return &contentItem{kind: "tool-result", call: c, output: v, outputUndefined: v == nil, toModel: tool.ToModelOutput}
}

// toolOutput is createToolModelOutput: an error as String(error), the tool's
// toModelOutput (MCP tools), else text for a string and JSON for the rest.
func toolOutput(item *contentItem) models.ToolOutput {
	if item.kind == "tool-error" {
		return models.OutputErrorText(ErrorString(item.err))
	}
	if item.toModel != nil {
		return item.toModel(item.output)
	}
	if s, ok := item.output.(string); ok {
		return models.OutputText(s)
	}
	if item.outputUndefined {
		return models.OutputJSON(nil)
	}
	return models.OutputJSON(json.RawMessage(toJSONValue(item.output)))
}

// toJSONValue is JSON.parse(JSON.stringify(v)) written back as JSON text.
func toJSONValue(v any) string {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return "null"
	}
	parsed, err := jsonx.Parse(b)
	if err != nil {
		return "null"
	}
	return jsonx.Stringify(parsed)
}

func callInput(c *toolCall) any {
	if c.invalid {
		if _, ok := c.input.(string); ok {
			return jsonx.NewMap[any]()
		}
	}
	if c.input == nil {
		return json.RawMessage("null")
	}
	return c.input
}

// toResponseMessages: the step's assistant message and its tool message.
func toResponseMessages(content []*contentItem) []models.Message {
	var parts []models.Part
	order := map[string]int{}
	for _, c := range content {
		switch c.kind {
		case "text":
			text := c.text.String()
			if text == "" && c.po == nil {
				continue
			}
			p := models.TextPart(text)
			if c.po != nil {
				p.SetProviderOptions(c.po)
			}
			parts = append(parts, p)
		case "reasoning":
			p := models.ReasoningPart(c.text.String())
			if c.po != nil {
				p.SetProviderOptions(c.po)
			}
			parts = append(parts, p)
		case "tool-call":
			if _, ok := order[c.call.id]; !ok {
				order[c.call.id] = len(order)
			}
			parts = append(parts, models.ToolCallPart(c.call.id, c.call.name, callInput(c.call)))
		case "tool-approval-request":
			parts = append(parts, models.ToolApprovalRequestPart(c.approvalID, c.call.id))
		}
	}
	var out []models.Message
	if len(parts) > 0 {
		out = append(out, models.AssistantParts(parts...))
	}
	type indexed struct {
		part models.Part
		id   string
		i    int
	}
	var results []indexed
	for _, c := range content {
		if c.kind != "tool-result" && c.kind != "tool-error" {
			continue
		}
		results = append(results, indexed{models.ToolResultPart(c.call.id, c.call.name, toolOutput(c)), c.call.id, len(results)})
	}
	if len(results) > 0 {
		sorted := append([]indexed(nil), results...)
		rank := func(x indexed) (int, bool) { r, ok := order[x.id]; return r, ok }
		for i := 1; i < len(sorted); i++ {
			for j := i; j > 0; j-- {
				a, b := sorted[j-1], sorted[j]
				ra, oka := rank(a)
				rb, okb := rank(b)
				less := false
				switch {
				case !oka && !okb:
					less = b.i < a.i
				case !oka:
					less = true
				case !okb:
					less = false
				default:
					less = rb < ra || (rb == ra && b.i < a.i)
				}
				if !less {
					break
				}
				sorted[j-1], sorted[j] = b, a
			}
		}
		tp := make([]models.Part, len(sorted))
		for i, s := range sorted {
			tp[i] = s.part
		}
		out = append(out, models.ToolMessage(tp...))
	}
	return out
}

// --- resume -------------------------------------------------------------------------------

type collectedApproval struct {
	call     models.Part
	response models.Part
	existing *models.Part
}

// resumeApprovals is collectToolApprovals plus the tool execution streamText
// does before its first step when the last message answers approvals. It
// returns the tool message that joins the response messages.
func (l *loop) resumeApprovals(messages []models.Message) (*models.Message, error) {
	last := messages[len(messages)-1]
	callsByID := map[string]models.Part{}
	callOrder := map[string]int{}
	n := 0
	requests := map[string]models.Part{}
	for _, m := range messages {
		if m.Role() != "assistant" || !m.HasParts() {
			continue
		}
		for _, p := range m.Parts() {
			switch p.Type() {
			case "tool-call":
				callsByID[p.ToolCallID()] = p
				callOrder[p.ToolCallID()] = n
				n++
			case "tool-approval-request":
				requests[p.ApprovalID()] = p
			}
		}
	}
	existing := map[string]models.Part{}
	for _, p := range last.Parts() {
		if p.Type() == "tool-result" {
			existing[p.ToolCallID()] = p
		}
	}
	var approved, denied []collectedApproval
	collected := map[string]bool{}
	for _, resp := range last.Parts() {
		if resp.Type() != "tool-approval-response" {
			continue
		}
		req, ok := requests[resp.ApprovalID()]
		if !ok {
			return nil, l.emit(Part{Type: "error", Err: fmt.Errorf("Tool approval response references unknown approvalId: '%s'. No matching tool-approval-request found in message history.", resp.ApprovalID())})
		}
		var ex *models.Part
		if p, ok := existing[req.ToolCallID()]; ok {
			if resp.Approved() || p.Output().Type() != "execution-denied" {
				continue
			}
			ex = &p
		}
		call, ok := callsByID[req.ToolCallID()]
		if !ok {
			return nil, l.emit(Part{Type: "error", Err: fmt.Errorf("Tool call %s not found for approval request %s.", req.ToolCallID(), req.ApprovalID())})
		}
		if collected[call.ToolCallID()] {
			continue
		}
		collected[call.ToolCallID()] = true
		a := collectedApproval{call: call, response: resp, existing: ex}
		if resp.Approved() {
			approved = append(approved, a)
		} else {
			denied = append(denied, a)
		}
	}
	// Stable sort by call order.
	for i := 1; i < len(approved); i++ {
		for j := i; j > 0 && callOrder[approved[j].call.ToolCallID()] < callOrder[approved[j-1].call.ToolCallID()]; j-- {
			approved[j-1], approved[j] = approved[j], approved[j-1]
		}
	}
	if len(approved) == 0 && len(denied) == 0 {
		return nil, nil
	}
	// validateApprovedToolApprovals asks toolApproval again for each approved
	// call (a Palui policy never answers "denied", but it taints the turn for
	// an untrusted tool, as the TS callback does).
	if l.opts.ToolApproval != nil {
		for _, a := range approved {
			v, err := jsonx.Parse(a.call.Input())
			if err != nil {
				v = nil
			}
			l.opts.ToolApproval(ToolCallView{ToolCallID: a.call.ToolCallID(), ToolName: a.call.ToolName(), Input: v, InputJSON: jsonx.Stringify(v)})
		}
	}
	for _, d := range denied {
		if err := l.emit(Part{Type: "tool-output-denied", ToolCallID: d.call.ToolCallID(), ToolName: d.call.ToolName()}); err != nil {
			return nil, err
		}
	}
	var calls []*toolCall
	for _, a := range approved {
		tool, ok := l.opts.Tools.Get(a.call.ToolName())
		if !ok || tool.Execute == nil {
			continue
		}
		v, err := jsonx.Parse(a.call.Input())
		if err != nil {
			v = nil
		}
		calls = append(calls, &toolCall{id: a.call.ToolCallID(), name: a.call.ToolName(), input: v, json: jsonx.Stringify(v)})
	}
	results := l.execute(calls, messages)
	var parts []models.Part
	for _, r := range results {
		if r == nil {
			return nil, l.emit(Part{})
		}
		p := Part{ToolCallID: r.call.id, ToolName: r.call.name, Input: r.call.input, InputJSON: r.call.json}
		if r.kind == "tool-error" {
			p.Type, p.Err = "tool-error", r.err
		} else {
			p.Type, p.Output, p.OutputUndefined = "tool-result", r.output, r.outputUndefined
		}
		if err := l.emit(p); err != nil {
			return nil, err
		}
		parts = append(parts, models.ToolResultPart(r.call.id, r.call.name, toolOutput(r)))
	}
	for _, d := range denied {
		if d.existing != nil {
			continue
		}
		parts = append(parts, models.ToolResultPart(d.call.ToolCallID(), d.call.ToolName(), models.OutputDenied(d.response.Reason())))
	}
	if len(parts) == 0 {
		return nil, nil
	}
	m := models.ToolMessage(parts...)
	return &m, nil
}
