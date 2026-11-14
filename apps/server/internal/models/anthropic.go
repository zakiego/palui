package models

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
	"github.com/zakiego/palui/apps/server/internal/strx"
)

// The Anthropic Messages client (POST {baseURL}/messages, streaming). The
// request is what @ai-sdk/anthropic 4.0.99 sends for the same call options:
// model capabilities, reasoning to thinking/effort, cache_control, tool
// definitions with eager input streaming, betas in the anthropic-beta header.

type AnthropicConfig struct {
	// BaseURL including the version path, e.g. "http://host:8317/v1".
	BaseURL string
	APIKey  string
	// Provider name; default "anthropic.messages".
	Name string
	// HTTP client; default http.DefaultClient.
	Client *http.Client
}

type AnthropicModel struct {
	cfg   AnthropicConfig
	model string
}

// NewAnthropic is createAnthropic({ baseURL, apiKey, fetch })(modelId).
func NewAnthropic(cfg AnthropicConfig, modelID string) *AnthropicModel {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Name == "" {
		cfg.Name = "anthropic.messages"
	}
	return &AnthropicModel{cfg: cfg, model: modelID}
}

func (m *AnthropicModel) Provider() string { return m.cfg.Name }
func (m *AnthropicModel) ModelID() string  { return m.model }

// --- Capabilities ----------------------------------------------------------------------

type anthropicCaps struct {
	maxOutputTokens          int
	nativeStructuredOutput   bool
	adaptiveThinking         bool
	samplingParameters       bool
	xhighEffort              bool
	thinkingDisableAboveHigh bool
	thinkingCanBeDisabled    bool
	budgetThinking           bool
	forcedToolUse            bool
	betweenToolsThinking     bool
	knownModel               bool
}

func runtimeDefault() anthropicCaps {
	return anthropicCaps{maxOutputTokens: 4096, samplingParameters: true, thinkingDisableAboveHigh: true, thinkingCanBeDisabled: true, budgetThinking: true, forcedToolUse: true, knownModel: true}
}

func currentClaude() anthropicCaps {
	c := runtimeDefault()
	c.maxOutputTokens, c.nativeStructuredOutput, c.adaptiveThinking = 128000, true, true
	return c
}

func currentClaudeWithoutSampling() anthropicCaps {
	c := currentClaude()
	c.samplingParameters, c.xhighEffort = false, true
	return c
}

var (
	sonnet4Re  = regexp.MustCompile(`claude-sonnet-4(?:-|@)`)
	opus4Re    = regexp.MustCompile(`claude-opus-4(?:-|@)`)
	legacyRe   = regexp.MustCompile(`claude-(?:instant(?:-|$)|v?2(?:$|[-.:])|3(?:$|[-.]))`)
	claude3Hai = "claude-3-haiku"
)

// capsFor is getAnthropicModelCapabilities (the runtime rules, in order).
func capsFor(id string) anthropicCaps {
	has := func(s string) bool { return strings.Contains(id, s) }
	switch {
	case has("claude-sonnet-5-5"):
		c := currentClaudeWithoutSampling()
		c.thinkingDisableAboveHigh, c.thinkingCanBeDisabled, c.budgetThinking, c.forcedToolUse, c.betweenToolsThinking = false, false, false, false, true
		return c
	case has("claude-opus-5-5"):
		c := currentClaudeWithoutSampling()
		c.thinkingDisableAboveHigh, c.thinkingCanBeDisabled, c.budgetThinking, c.forcedToolUse = false, false, false, false
		return c
	case has("claude-haiku-5-5"):
		c := currentClaudeWithoutSampling()
		c.thinkingDisableAboveHigh, c.budgetThinking = false, false
		return c
	case has("claude-opus-5"):
		c := currentClaudeWithoutSampling()
		c.thinkingDisableAboveHigh = false
		return c
	case has("claude-fable-5-1"):
		c := currentClaudeWithoutSampling()
		c.thinkingCanBeDisabled, c.budgetThinking, c.forcedToolUse = false, false, false
		return c
	case has("claude-fable-5"):
		c := currentClaudeWithoutSampling()
		c.thinkingCanBeDisabled, c.budgetThinking = false, false
		return c
	case has("claude-opus-4-8") || has("claude-opus-4-7") || has("claude-sonnet-5"):
		return currentClaudeWithoutSampling()
	case has("claude-sonnet-4-6") || has("claude-opus-4-6"):
		return currentClaude()
	case has("claude-sonnet-4-5") || has("claude-opus-4-5") || has("claude-haiku-4-5"):
		c := runtimeDefault()
		c.maxOutputTokens, c.nativeStructuredOutput = 64000, true
		return c
	case has("claude-opus-4-1"):
		c := runtimeDefault()
		c.maxOutputTokens, c.nativeStructuredOutput = 32000, true
		return c
	case sonnet4Re.MatchString(id):
		c := runtimeDefault()
		c.maxOutputTokens = 64000
		return c
	case opus4Re.MatchString(id):
		c := runtimeDefault()
		c.maxOutputTokens = 32000
		return c
	case has(claude3Hai):
		return runtimeDefault()
	case legacyRe.MatchString(id):
		c := runtimeDefault()
		c.knownModel = false
		return c
	case has("claude-"):
		c := currentClaudeWithoutSampling()
		c.thinkingDisableAboveHigh, c.knownModel = false, false
		return c
	}
	c := runtimeDefault()
	c.knownModel = false
	return c
}

var reasoningLevels = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

var budgetPercent = map[string]float64{"minimal": .02, "low": .1, "medium": .3, "high": .6, "xhigh": .9, "max": .95}

// mapEffort is mapReasoningToProviderEffort: the level itself, else the next
// lower supported one, else the next higher.
func mapEffort(reasoning string, supported map[string]bool) string {
	idx := -1
	for i, l := range reasoningLevels {
		if l == reasoning {
			idx = i
		}
	}
	candidates := []string{reasoning}
	for i := max(idx, 0) - 1; i >= 0; i-- {
		candidates = append(candidates, reasoningLevels[i])
	}
	candidates = append(candidates, reasoningLevels[idx+1:]...)
	for _, c := range candidates {
		if supported[c] {
			return c
		}
	}
	return ""
}

type thinkingConfig struct {
	Type         string
	BudgetTokens *int
	Display      string
}

// resolveReasoning is resolveAnthropicReasoningConfig.
func resolveReasoning(reasoning string, caps anthropicCaps) (*thinkingConfig, string) {
	if reasoning == "" || reasoning == "provider-default" {
		return nil, ""
	}
	if reasoning == "none" {
		if caps.betweenToolsThinking {
			return &thinkingConfig{Type: "between_tools"}, ""
		}
		if !caps.thinkingCanBeDisabled {
			return nil, "low"
		}
		return &thinkingConfig{Type: "disabled"}, ""
	}
	if caps.adaptiveThinking {
		supported := map[string]bool{"low": true, "medium": true, "high": true, "max": true}
		if caps.xhighEffort {
			supported["xhigh"] = true
		}
		return &thinkingConfig{Type: "adaptive", Display: "summarized"}, mapEffort(reasoning, supported)
	}
	pct := budgetPercent[reasoning]
	b := int(math.Min(float64(caps.maxOutputTokens), math.Max(1024, math.Floor(float64(caps.maxOutputTokens)*pct+0.5))))
	return &thinkingConfig{Type: "enabled", BudgetTokens: &b}, ""
}

// --- Request ---------------------------------------------------------------------------

const maxCacheBreakpoints = 4

type cacheValidator struct{ count int }

// get is CacheControlValidator.getCacheControl: the breakpoint in
// providerOptions.anthropic, counted, dropped past the fourth.
func (v *cacheValidator) get(po *jsonx.Object, canCache bool) any {
	if po == nil {
		return nil
	}
	a, ok := po.Get("anthropic")
	if !ok {
		return nil
	}
	am, ok := a.(*jsonx.Object)
	if !ok {
		return nil
	}
	cc, ok := am.Get("cacheControl")
	if !ok || cc == nil {
		cc, ok = am.Get("cache_control")
	}
	if !ok || cc == nil || !jsonx.Truthy(cc) {
		return nil
	}
	if !canCache {
		return nil
	}
	v.count++
	if v.count > maxCacheBreakpoints {
		return nil
	}
	return cc
}

// betaSet keeps betas unique, in insertion order.
type betaSet struct{ list []string }

func (b *betaSet) add(s string) {
	for _, x := range b.list {
		if x == s {
			return
		}
	}
	b.list = append(b.list, s)
}

func obj(kv ...any) *jsonx.Object {
	o := jsonx.NewMap[any]()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == nil {
			continue
		}
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func rawValue(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	v, err := jsonx.Parse(raw)
	if err != nil {
		return nil
	}
	return v
}

type anthropicBlock struct {
	typ  string
	msgs []Message
}

func groupBlocks(prompt []Message) []anthropicBlock {
	var blocks []anthropicBlock
	for _, m := range prompt {
		typ := m.Role()
		if typ == "tool" {
			typ = "user"
		}
		if len(blocks) == 0 || blocks[len(blocks)-1].typ != typ {
			blocks = append(blocks, anthropicBlock{typ: typ})
		}
		blocks[len(blocks)-1].msgs = append(blocks[len(blocks)-1].msgs, m)
	}
	return blocks
}

// toolInput is toAnthropicToolInput.
func toolInput(raw json.RawMessage) any {
	v := rawValue(raw)
	if s, ok := v.(string); ok {
		if parsed, err := jsonx.Parse([]byte(s)); err == nil {
			if o, ok := parsed.(*jsonx.Object); ok {
				return o
			}
		}
	}
	if o, ok := v.(*jsonx.Object); ok {
		return o
	}
	return obj("rawInvalidInput", v)
}

// UnsupportedError is the AI SDK's UnsupportedFunctionalityError.
type UnsupportedError struct{ Functionality string }

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("'%s' functionality not supported.", e.Functionality)
}

func decodeText(b64 string) string {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		b, _ = base64.RawStdEncoding.DecodeString(strings.TrimRight(b64, "="))
	}
	return strx.TextDecode(b)
}

// stringify is JSON.stringify of a raw JSON value (re-encoded compactly).
func stringify(raw json.RawMessage) string {
	if raw == nil {
		return "undefined"
	}
	v, err := jsonx.Parse(raw)
	if err != nil {
		return string(raw)
	}
	return jsonx.Stringify(v)
}

func outputValueString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// convertAnthropicPrompt is convertToAnthropicPrompt for the parts Palui sends.
func convertAnthropicPrompt(prompt []Message, v *cacheValidator) (system []any, messages []any, betas betaSet, err error) {
	blocks := groupBlocks(prompt)
	systemSet := false
	for i, block := range blocks {
		isLastBlock := i == len(blocks)-1
		switch block.typ {
		case "system":
			var content []any
			for _, m := range block.msgs {
				text, _ := m.Text()
				content = append(content, obj("type", "text", "text", text, "cache_control", v.get(m.ProviderOptions(), true)))
			}
			if i == 0 || !systemSet {
				system = append(system, content...)
				systemSet = true
			} else {
				betas.add("mid-conversation-system-2026-04-07")
				for _, c := range content {
					messages = append(messages, obj("role", "system", "content", []any{c}))
				}
			}
		case "user":
			content := []any{}
			for _, m := range block.msgs {
				parts := m.Parts()
				if m.Role() == "user" {
					for j, p := range parts {
						cc := v.get(p.ProviderOptions(), true)
						if cc == nil && j == len(parts)-1 {
							cc = v.get(m.ProviderOptions(), true)
						}
						switch p.Type() {
						case "text":
							content = append(content, obj("type", "text", "text", p.Text(), "cache_control", cc))
						case "file":
							c, err := anthropicFile(p, cc, &betas)
							if err != nil {
								return nil, nil, betas, err
							}
							content = append(content, c)
						}
					}
					continue
				}
				for j, p := range parts {
					if p.Type() == "tool-approval-response" {
						continue
					}
					out := p.Output()
					cc := v.get(p.ProviderOptions(), true)
					if cc == nil {
						cc = v.get(out.ProviderOptions(), true)
					}
					if cc == nil && j == len(parts)-1 {
						cc = v.get(m.ProviderOptions(), true)
					}
					var value any
					switch out.Type() {
					case "content":
						value = toolContent(out.Value(), &betas)
					case "text", "error-text":
						value = outputValueString(out.Value())
					case "execution-denied":
						if r := out.Reason(); r != nil {
							value = *r
						} else {
							value = "Tool call execution denied."
						}
					default:
						value = stringify(out.Value())
					}
					var isError any
					if out.Type() == "error-text" || out.Type() == "error-json" {
						isError = true
					}
					content = append(content, obj("type", "tool_result", "tool_use_id", p.ToolCallID(), "content", value, "is_error", isError, "cache_control", cc))
				}
			}
			messages = append(messages, obj("role", "user", "content", content))
		case "assistant":
			var content []*jsonx.Object
			for j, m := range block.msgs {
				isLastMessage := j == len(block.msgs)-1
				var parts []Part
				for _, p := range m.Parts() {
					if p.Type() == "text" && p.Text() == "" {
						continue
					}
					parts = append(parts, p)
				}
				for k, p := range parts {
					isLastPart := k == len(parts)-1
					cc := v.get(p.ProviderOptions(), true)
					if cc == nil && isLastPart {
						cc = v.get(m.ProviderOptions(), true)
					}
					switch p.Type() {
					case "text":
						text := p.Text()
						if isLastBlock && isLastMessage && isLastPart {
							text = strx.Trim(text)
						}
						content = append(content, obj("type", "text", "text", text, "cache_control", cc))
					case "reasoning":
						var a *jsonx.Object
						if po := p.ProviderOptions(); po != nil {
							if x, ok := po.Get("anthropic"); ok {
								a, _ = x.(*jsonx.Object)
							}
						}
						if a == nil {
							continue
						}
						if sig, ok := a.Get("signature"); ok && sig != nil {
							content = append(content, obj("type", "thinking", "thinking", p.Text(), "signature", sig))
						} else if red, ok := a.Get("redactedData"); ok && red != nil {
							content = append(content, obj("type", "redacted_thinking", "data", red))
						}
					case "tool-call":
						if p.ProviderExecuted() {
							continue
						}
						content = append(content, obj("type", "tool_use", "id", p.ToolCallID(), "name", p.ToolName(), "input", toolInput(p.Input()), "cache_control", cc))
					}
				}
			}
			if len(content) > 0 {
				messages = append(messages, obj("role", "assistant", "content", moveToolUseToEnd(content)))
			}
		}
	}
	return system, messages, betas, nil
}

func blockType(o *jsonx.Object) string {
	t, _ := o.Get("type")
	s, _ := t.(string)
	return s
}

func moveToolUseToEnd(content []*jsonx.Object) []any {
	var out, segment []*jsonx.Object
	flush := func() {
		for _, p := range segment {
			if blockType(p) != "tool_use" {
				out = append(out, p)
			}
		}
		for _, p := range segment {
			if blockType(p) == "tool_use" {
				out = append(out, p)
			}
		}
		segment = nil
	}
	for _, p := range content {
		switch blockType(p) {
		case "thinking", "redacted_thinking", "fallback":
			flush()
			out = append(out, p)
		default:
			segment = append(segment, p)
		}
	}
	flush()
	res := make([]any, len(out))
	for i, p := range out {
		res[i] = p
	}
	return res
}

func anthropicFile(p Part, cc any, betas *betaSet) (any, error) {
	d := partFileData(p)
	var title any
	if fn := p.Filename(); fn != nil {
		title = *fn
	}
	switch d.Type {
	case "text":
		return obj("type", "document", "source", obj("type", "text", "media_type", "text/plain", "data", d.Text), "title", title, "cache_control", cc), nil
	case "url", "data":
		mt := p.MediaType()
		if topLevel(mt) == "image" {
			var source any
			if d.Type == "url" {
				source = obj("type", "url", "url", d.URL)
			} else {
				full, err := fullMediaType(p)
				if err != nil {
					return nil, err
				}
				source = obj("type", "base64", "media_type", full, "data", d.Data)
			}
			return obj("type", "image", "source", source, "cache_control", cc), nil
		}
		isPDF := false
		if topLevel(mt) == "application" {
			if d.Type == "url" {
				isPDF = mt == "application/pdf"
			} else if full, err := fullMediaType(p); err == nil {
				isPDF = full == "application/pdf"
			}
		}
		if isPDF {
			betas.add("pdfs-2024-09-25")
			var source any
			if d.Type == "url" {
				source = obj("type", "url", "url", d.URL)
			} else {
				source = obj("type", "base64", "media_type", "application/pdf", "data", d.Data)
			}
			return obj("type", "document", "source", source, "title", title, "cache_control", cc), nil
		}
		if mt == "text/plain" {
			var source any
			if d.Type == "url" {
				source = obj("type", "url", "url", d.URL)
			} else {
				source = obj("type", "text", "media_type", "text/plain", "data", decodeText(d.Data))
			}
			return obj("type", "document", "source", source, "title", title, "cache_control", cc), nil
		}
		return nil, &UnsupportedError{Functionality: "media type: " + mt}
	}
	return nil, &UnsupportedError{Functionality: "file data type: " + d.Type}
}

// toolContent converts a "content" tool output.
func toolContent(raw json.RawMessage, betas *betaSet) []any {
	var items []Part
	_ = json.Unmarshal(raw, &items)
	out := []any{}
	for _, it := range items {
		switch it.Type() {
		case "text":
			out = append(out, obj("type", "text", "text", it.Text()))
		case "file":
			d := partFileData(it)
			isImage := topLevel(it.MediaType()) == "image"
			switch d.Type {
			case "url":
				if isImage {
					out = append(out, obj("type", "image", "source", obj("type", "url", "url", d.URL)))
				} else {
					out = append(out, obj("type", "document", "source", obj("type", "url", "url", d.URL)))
				}
			case "data":
				if isImage {
					full, err := fullMediaType(it)
					if err != nil {
						continue
					}
					out = append(out, obj("type", "image", "source", obj("type", "base64", "media_type", full, "data", d.Data)))
				} else if full, err := fullMediaType(it); err == nil && full == "application/pdf" {
					betas.add("pdfs-2024-09-25")
					out = append(out, obj("type", "document", "source", obj("type", "base64", "media_type", "application/pdf", "data", d.Data)))
				}
			}
		}
	}
	return out
}

// AnthropicRequest is the request a call sends: URL, headers and body.
type AnthropicRequest struct {
	URL     string
	Headers map[string]string
	Body    *jsonx.Object
}

// BuildRequest is prepareRequest plus the headers.
func (m *AnthropicModel) BuildRequest(opts CallOptions) (AnthropicRequest, error) {
	prompt, err := Standardize(opts.Prompt)
	if err != nil {
		return AnthropicRequest{}, err
	}
	caps := capsFor(m.model)
	temperature := opts.Temperature
	if temperature != nil {
		t := *temperature
		if t > 1 {
			t = 1
		} else if t < 0 {
			t = 0
		}
		temperature = &t
	}
	if !caps.samplingParameters {
		temperature = nil
	}
	v := &cacheValidator{}
	system, messages, betas, err := convertAnthropicPrompt(prompt, v)
	if err != nil {
		return AnthropicRequest{}, err
	}
	thinking, effort := resolveReasoning(opts.Reasoning, caps)
	if thinking != nil && thinking.Type == "disabled" {
		effort = ""
	}
	if !caps.thinkingCanBeDisabled && thinking != nil && thinking.Type == "disabled" {
		if caps.betweenToolsThinking {
			thinking = &thinkingConfig{Type: "between_tools"}
		} else {
			thinking = nil
		}
	}
	if !caps.budgetThinking && thinking != nil && thinking.Type == "enabled" {
		thinking = &thinkingConfig{Type: "adaptive"}
	}
	if thinking != nil && thinking.Type == "between_tools" && (effort == "xhigh" || effort == "max") {
		effort = "high"
	}
	thinkingType := ""
	if thinking != nil {
		thinkingType = thinking.Type
	}
	isThinking := thinkingType == "enabled" || thinkingType == "adaptive" || thinkingType == "between_tools"
	budget := 0
	if thinkingType == "enabled" && thinking.BudgetTokens != nil {
		budget = *thinking.BudgetTokens
	}
	maxTokens := caps.maxOutputTokens
	if opts.MaxOutputTokens != nil {
		maxTokens = *opts.MaxOutputTokens
	}
	var thinkingObj any
	if isThinking || thinkingType == "disabled" {
		t := obj("type", thinkingType)
		if thinkingType == "enabled" {
			t.Set("budget_tokens", budget)
		}
		if thinkingType == "adaptive" && thinking.Display != "" {
			t.Set("display", thinking.Display)
		}
		thinkingObj = t
	}
	if isThinking {
		temperature = nil
		maxTokens += budget
	}
	if caps.knownModel && maxTokens > caps.maxOutputTokens {
		maxTokens = caps.maxOutputTokens
	}
	body := jsonx.NewMap[any]()
	body.Set("model", m.model)
	body.Set("max_tokens", maxTokens)
	if temperature != nil {
		body.Set("temperature", *temperature)
	}
	if thinkingObj != nil {
		body.Set("thinking", thinkingObj)
	}
	// Structured output (responseFormat): native output_config.format, or
	// the JSON tool fallback.
	rf := opts.ResponseFormat
	useStructured := false
	if rf.hasSchema() {
		schemaOK := nativeSchemaOK(rf.Schema)
		useStructured = caps.nativeStructuredOutput && schemaOK
		if !useStructured && !caps.forcedToolUse && caps.nativeStructuredOutput && schemaOK {
			useStructured = true
		}
	}
	jsonTool := rf.hasSchema() && !useStructured
	if effort != "" || useStructured {
		oc := jsonx.NewMap[any]()
		if effort != "" {
			oc.Set("effort", effort)
		}
		if useStructured {
			schema, err := SanitizeJSONSchema(rf.Schema)
			if err != nil {
				return AnthropicRequest{}, err
			}
			oc.Set("format", obj("type", "json_schema", "schema", schema))
		}
		body.Set("output_config", oc)
	}
	if system != nil {
		body.Set("system", system)
	}
	if messages == nil {
		messages = []any{}
	}
	body.Set("messages", messages)

	// Tools (prepareTools).
	offered := opts.offeredTools()
	var toolBetas betaSet
	disableParallel := false
	if jsonTool {
		var kept []ToolDef
		for _, t := range offered {
			if opts.ToolChoice != nil && (opts.ToolChoice.Type == "none" || (opts.ToolChoice.Type == "tool" && t.Name != opts.ToolChoice.ToolName)) {
				continue
			}
			kept = append(kept, t)
		}
		offered = append(kept, ToolDef{Name: JSONToolName, Description: JSONToolDescription, InputSchema: rf.Schema})
		opts.ToolChoice = &ToolChoice{Type: "required"}
		disableParallel = true
	}
	if len(offered) > 0 {
		tools := make([]any, 0, len(offered))
		for _, t := range offered {
			var desc any
			if t.Description != "" {
				desc = t.Description
			}
			def := obj("name", t.Name, "description", desc, "input_schema", rawValue(t.InputSchema), "cache_control", v.get(t.ProviderOptions, true))
			def.Set("eager_input_streaming", true)
			if caps.nativeStructuredOutput && t.Strict != nil {
				def.Set("strict", *t.Strict)
			}
			tools = append(tools, def)
			if caps.nativeStructuredOutput && !jsonTool {
				toolBetas.add("structured-outputs-2025-11-13")
			}
		}
		choice := opts.ToolChoice
		if choice == nil {
			choice = ChoiceAuto
		}
		rejectsForced := !caps.forcedToolUse || thinkingType == "enabled"
		var toolChoice any
		switch choice.Type {
		case "auto":
			toolChoice = obj("type", "auto")
		case "none":
			toolChoice = obj("type", "none")
		case "required":
			if rejectsForced {
				toolChoice = obj("type", "auto")
			} else {
				toolChoice = obj("type", "any")
			}
			if disableParallel {
				toolChoice.(*jsonx.Object).Set("disable_parallel_tool_use", true)
			}
		case "tool":
			if rejectsForced {
				var only []any
				for i, t := range offered {
					if t.Name == choice.ToolName {
						only = append(only, tools[i])
					}
				}
				tools = only
				toolChoice = obj("type", "auto")
			} else {
				toolChoice = obj("type", "tool", "name", choice.ToolName)
			}
		}
		body.Set("tools", tools)
		body.Set("tool_choice", toolChoice)
	}
	body.Set("stream", true)

	headers := map[string]string{"anthropic-version": "2023-06-01", "x-api-key": m.cfg.APIKey}
	all := betaSet{}
	for _, b := range betas.list {
		all.add(b)
	}
	for _, b := range toolBetas.list {
		all.add(b)
	}
	if len(all.list) > 0 {
		headers["anthropic-beta"] = strings.Join(all.list, ",")
	}
	for k, val := range opts.Headers {
		headers[strings.ToLower(k)] = val
	}
	return AnthropicRequest{URL: m.cfg.BaseURL + "/messages", Headers: headers, Body: body}, nil
}

func readAnthropicError(raw []byte) (string, bool) { return readErrorMessage(raw) }

// --- Stream ----------------------------------------------------------------------------

func (m *AnthropicModel) Stream(ctx context.Context, opts CallOptions) (Stream, error) {
	req, err := m.BuildRequest(opts)
	if err != nil {
		return nil, err
	}
	res, err := postJSON(ctx, m.cfg.Client, req.URL, req.Headers, req.Body, readAnthropicError)
	if err != nil {
		return nil, err
	}
	sse := newSSE(res.Body)
	p := &anthropicParser{blocks: map[int]*anthropicBlockState{}}
	stream := &eventStream{closer: sse.close}
	stream.produce = func() ([]Event, bool) {
		data, err := sse.next()
		if err != nil {
			if isCanceled(ctx, err) {
				return []Event{{Type: EventError, Err: ctx.Err()}}, false
			}
			if !errors.Is(err, io.EOF) {
				return p.track([]Event{{Type: EventError, Err: err}}), false
			}
			return p.flush(), false
		}
		return p.chunk(data), true
	}
	// The first event (pings make none): an error there fails the call, so
	// failover can try the next link.
	ev, err := stream.Recv()
	if err != nil {
		return stream, nil
	}
	if ev.Type == EventError {
		sse.close()
		var se *StreamError
		if asStreamError(ev.Err, &se) {
			code := se.StatusCode
			if code == 0 {
				code = 500
			}
			return nil, &APICallError{Message: se.Message, URL: req.URL, StatusCode: code, ResponseBody: string(se.Data), IsRetryable: se.IsRetryable}
		}
		return nil, ev.Err
	}
	stream.queue = append([]Event{ev}, stream.queue...)
	return stream, nil
}

func asStreamError(err error, target **StreamError) bool {
	se, ok := err.(*StreamError)
	if ok {
		*target = se
	}
	return ok
}

type anthropicBlockState struct {
	typ        string // text, reasoning, tool-call
	toolCallID string
	toolName   string
	input      string
}

type anthropicParser struct {
	blocks      map[int]*anthropicBlockState
	blockType   string
	usage       struct{ input, output, cacheRead, cacheWrite int64 }
	reasoning   *int64
	finish      FinishReason
	finished    bool
	lastFinish  *Event
	hasOutput   bool
	streamError bool
	open        bool
	messageID   string
	invalid     bool
}

func streamErrorMeta(typ string) (int, bool) {
	switch typ {
	case "api_error":
		return 500, true
	case "overloaded_error":
		return 529, true
	case "rate_limit_error":
		return 429, true
	case "request_too_large":
		return 413, false
	case "authentication_error":
		return 401, false
	case "permission_error":
		return 403, false
	case "not_found_error":
		return 404, false
	case "billing_error", "invalid_request_error":
		return 400, false
	}
	return 0, false
}

func mapAnthropicStop(reason string) string {
	switch reason {
	case "pause_turn", "end_turn", "stop_sequence":
		return "stop"
	case "refusal":
		return "content-filter"
	case "tool_use":
		return "tool-calls"
	case "max_tokens", "model_context_window_exceeded":
		return "length"
	}
	return "other"
}

func (p *anthropicParser) usageOut() Usage {
	u := Usage{}
	u.InputTokens.Total = i64(p.usage.input + p.usage.cacheWrite + p.usage.cacheRead)
	u.InputTokens.NoCache = i64(p.usage.input)
	u.InputTokens.CacheRead = i64(p.usage.cacheRead)
	u.InputTokens.CacheWrite = i64(p.usage.cacheWrite)
	u.OutputTokens.Total = i64(p.usage.output)
	if p.reasoning != nil {
		u.OutputTokens.Text = i64(p.usage.output - *p.reasoning)
		u.OutputTokens.Reasoning = i64(*p.reasoning)
	}
	return u
}

type anthropicChunk struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	// message_start
	Message *struct {
		ID         *string `json:"id"`
		StopReason *string `json:"stop_reason"`
		Usage      struct {
			InputTokens int64  `json:"input_tokens"`
			CacheRead   *int64 `json:"cache_read_input_tokens"`
			CacheWrite  *int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
	// content_block_start
	ContentBlock *struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
		Data  *string         `json:"data"`
	} `json:"content_block"`
	// content_block_delta and message_delta
	Delta json.RawMessage `json:"delta"`
	Usage *struct {
		InputTokens   *int64 `json:"input_tokens"`
		OutputTokens  int64  `json:"output_tokens"`
		CacheRead     *int64 `json:"cache_read_input_tokens"`
		CacheWrite    *int64 `json:"cache_creation_input_tokens"`
		OutputDetails *struct {
			ThinkingTokens *int64 `json:"thinking_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func nonEmptyObject(raw json.RawMessage) bool {
	var o map[string]json.RawMessage
	return json.Unmarshal(raw, &o) == nil && len(o) > 0
}

func (p *anthropicParser) track(evs []Event) []Event {
	for _, ev := range evs {
		switch ev.Type {
		case EventFinish:
			p.hasOutput = false
			e := ev
			p.lastFinish = &e
		case EventError:
			p.streamError = true
		case EventTextDelta, EventReasoningDelta, EventToolInputDelta:
			p.hasOutput = p.hasOutput || len(ev.Delta) > 0
		case EventToolCall:
			p.hasOutput = true
		}
	}
	return evs
}

func (p *anthropicParser) chunk(data string) []Event {
	if p.invalid {
		return nil
	}
	var c anthropicChunk
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return p.track([]Event{{Type: EventError, Err: fmt.Errorf("Invalid JSON response: %s", err.Error())}})
	}
	return p.track(p.handle(c))
}

func (p *anthropicParser) handle(c anthropicChunk) []Event {
	idx := strconv.Itoa(c.Index)
	switch c.Type {
	case "ping":
		return nil
	case "content_block_start":
		b := c.ContentBlock
		if b == nil {
			return nil
		}
		p.blockType = b.Type
		switch b.Type {
		case "text":
			p.blocks[c.Index] = &anthropicBlockState{typ: "text"}
			return []Event{{Type: EventTextStart, ID: idx}}
		case "thinking":
			p.blocks[c.Index] = &anthropicBlockState{typ: "reasoning"}
			return []Event{{Type: EventReasoningStart, ID: idx}}
		case "redacted_thinking":
			p.blocks[c.Index] = &anthropicBlockState{typ: "reasoning"}
			var md *jsonx.Object
			if b.Data != nil {
				md = obj("anthropic", obj("redactedData", *b.Data))
			}
			return []Event{{Type: EventReasoningStart, ID: idx, ProviderMetadata: md}}
		case "tool_use":
			input := ""
			if nonEmptyObject(b.Input) {
				input = stringify(b.Input)
			}
			p.blocks[c.Index] = &anthropicBlockState{typ: "tool-call", toolCallID: b.ID, toolName: b.Name, input: input}
			return []Event{{Type: EventToolInputStart, ID: b.ID, ToolName: b.Name}}
		}
		return nil
	case "content_block_delta":
		var d struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			Signature   string `json:"signature"`
			PartialJSON string `json:"partial_json"`
		}
		_ = json.Unmarshal(c.Delta, &d)
		switch d.Type {
		case "text_delta":
			return []Event{{Type: EventTextDelta, ID: idx, Delta: d.Text}}
		case "thinking_delta":
			return []Event{{Type: EventReasoningDelta, ID: idx, Delta: d.Thinking}}
		case "signature_delta":
			if p.blockType == "thinking" {
				return []Event{{Type: EventReasoningDelta, ID: idx, Delta: "", ProviderMetadata: obj("anthropic", obj("signature", d.Signature))}}
			}
			return nil
		case "input_json_delta":
			b := p.blocks[c.Index]
			if d.PartialJSON == "" || b == nil || b.typ != "tool-call" {
				return nil
			}
			b.input += d.PartialJSON
			return []Event{{Type: EventToolInputDelta, ID: b.toolCallID, Delta: d.PartialJSON}}
		case "citations_delta":
			return nil
		}
		return []Event{{Type: EventError, Err: fmt.Errorf("Unsupported delta type: %s", d.Type)}}
	case "content_block_stop":
		b := p.blocks[c.Index]
		delete(p.blocks, c.Index)
		p.blockType = ""
		if b == nil {
			return nil
		}
		switch b.typ {
		case "text":
			return []Event{{Type: EventTextEnd, ID: idx}}
		case "reasoning":
			return []Event{{Type: EventReasoningEnd, ID: idx}}
		case "tool-call":
			input := b.input
			if input == "" {
				input = "{}"
			}
			return []Event{{Type: EventToolInputEnd, ID: b.toolCallID}, {Type: EventToolCall, ToolCallID: b.toolCallID, ToolName: b.toolName, Input: input}}
		}
		return nil
	case "message_start":
		msg := c.Message
		if msg == nil {
			return nil
		}
		id := ""
		if msg.ID != nil {
			id = *msg.ID
		}
		if p.open {
			if p.messageID == id {
				return nil
			}
			p.invalid = true
			return []Event{{Type: EventError, Err: fmt.Errorf("Received message_start for message %s while message %s is still open.", jsonx.Stringify(id), jsonx.Stringify(p.messageID))}}
		}
		p.open, p.messageID = true, id
		p.usage.input = msg.Usage.InputTokens
		p.usage.cacheRead = val(msg.Usage.CacheRead)
		p.usage.cacheWrite = val(msg.Usage.CacheWrite)
		if msg.StopReason != nil {
			r := *msg.StopReason
			p.finish = FinishReason{Unified: mapAnthropicStop(r), Raw: &r}
		}
		var out []Event
		for _, part := range msg.Content {
			if part.Type != "tool_use" {
				continue
			}
			in := "{}"
			if len(part.Input) > 0 && string(part.Input) != "null" {
				in = stringify(part.Input)
			}
			out = append(out, Event{Type: EventToolInputStart, ID: part.ID, ToolName: part.Name}, Event{Type: EventToolInputDelta, ID: part.ID, Delta: in}, Event{Type: EventToolInputEnd, ID: part.ID}, Event{Type: EventToolCall, ToolCallID: part.ID, ToolName: part.Name, Input: in})
		}
		return out
	case "message_delta":
		if u := c.Usage; u != nil {
			if u.InputTokens != nil && *u.InputTokens != p.usage.input {
				p.usage.input = *u.InputTokens
			}
			p.usage.output = u.OutputTokens
			if u.OutputDetails != nil {
				p.reasoning = u.OutputDetails.ThinkingTokens
			}
			if u.CacheRead != nil {
				p.usage.cacheRead = *u.CacheRead
			}
			if u.CacheWrite != nil {
				p.usage.cacheWrite = *u.CacheWrite
			}
		}
		var d struct {
			StopReason *string `json:"stop_reason"`
		}
		_ = json.Unmarshal(c.Delta, &d)
		raw := ""
		if d.StopReason != nil {
			raw = *d.StopReason
		}
		p.finish = FinishReason{Unified: mapAnthropicStop(raw), Raw: d.StopReason}
		return nil
	case "message_stop":
		p.open = false
		p.messageID = ""
		f := p.finish
		if f.Unified == "" {
			f.Unified = "other"
		}
		return []Event{{Type: EventFinish, FinishReason: f, Usage: p.usageOut()}}
	case "error":
		var e struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(c.Error, &e)
		code, retry := streamErrorMeta(e.Type)
		return []Event{{Type: EventError, Err: &StreamError{Message: e.Message, Type: e.Type, StatusCode: code, IsRetryable: retry, Data: c.Error}}}
	}
	return []Event{{Type: EventError, Err: fmt.Errorf("Unsupported chunk type: %s", c.Type)}}
}

// flush is the end of the stream: a stream cut off after output ends with an
// "interrupted" finish.
func (p *anthropicParser) flush() []Event {
	if p.hasOutput && !p.streamError {
		u := Usage{}
		if p.lastFinish != nil {
			u = p.lastFinish.Usage
		}
		return []Event{{Type: EventFinish, FinishReason: FinishReason{Unified: "interrupted"}, Usage: u}}
	}
	return nil
}
