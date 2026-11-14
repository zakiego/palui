package models

import (
	"strings"

	"github.com/zakiego/palui/apps/server/internal/jsonx"
)

// Prompt caching (PLAN 5.2). The turn sets provider-neutral marks on the
// points worth keeping; per model link they become Anthropic cache breakpoints
// (providerOptions.anthropic.cacheControl) or are dropped for every other
// protocol. Three breakpoints at most: the stable system block (1h), the
// newest user message (1h) and, from the second step on, the last tool result
// (5m). See cache.ts for why the lifetimes differ.

// CacheMark is the providerOptions key of the neutral mark.
const CacheMark = "palui"

// CacheMarkOptions is cacheMark(ttl): {palui: {cache: ttl}} for "5m" or "1h".
func CacheMarkOptions(ttl string) *jsonx.Object {
	inner := jsonx.NewMap[any]()
	inner.Set("cache", ttl)
	o := jsonx.NewMap[any]()
	o.Set(CacheMark, inner)
	return o
}

func SpeaksAnthropic(provider string) bool {
	return provider == "anthropic" || strings.HasPrefix(provider, "anthropic.")
}

func markOf(po *jsonx.Object) string {
	if po == nil {
		return ""
	}
	v, ok := po.Get(CacheMark)
	if !ok {
		return ""
	}
	m, ok := v.(*jsonx.Object)
	if !ok {
		return ""
	}
	c, _ := m.Get("cache")
	if s, ok := c.(string); ok && (s == "5m" || s == "1h") {
		return s
	}
	return ""
}

func cacheControl(ttl string) *jsonx.Object {
	o := jsonx.NewMap[any]()
	o.Set("type", "ephemeral")
	if ttl == "1h" {
		o.Set("ttl", ttl)
	}
	return o
}

// rewrite drops the neutral mark and, when ttl is set, adds the breakpoint.
func rewrite(obj Object, ttl string) Object {
	po := obj.ProviderOptions()
	rest := jsonx.NewMap[any]()
	if po != nil {
		for _, k := range po.Keys() {
			if k != CacheMark {
				v, _ := po.Get(k)
				rest.Set(k, v)
			}
		}
	}
	if ttl != "" {
		anthropic := jsonx.NewMap[any]()
		if v, ok := rest.Get("anthropic"); ok {
			if m, ok := v.(*jsonx.Object); ok {
				anthropic = m.Clone()
			}
		}
		anthropic.Set("cacheControl", cacheControl(ttl))
		rest.Set("anthropic", anthropic)
	}
	out := obj.Clone()
	out.Delete("providerOptions")
	if rest.Len() > 0 {
		out.Set("providerOptions", rest)
	}
	return out
}

// WithPromptCache is the call options for one model link. A prompt without
// marks is returned as it came.
func WithPromptCache(opts CallOptions, provider string) CallOptions {
	marked := false
	for _, m := range opts.Prompt {
		if markOf(m.ProviderOptions()) != "" {
			marked = true
			break
		}
		for _, p := range m.Parts() {
			if markOf(p.ProviderOptions()) != "" {
				marked = true
				break
			}
		}
	}
	if !marked {
		return opts
	}
	anthropic := SpeaksAnthropic(provider)
	ttlFor := func(ttl string) string {
		if anthropic {
			return ttl
		}
		return ""
	}
	out := make([]Message, len(opts.Prompt))
	for i, m := range opts.Prompt {
		msg := m
		if ttl := markOf(m.ProviderOptions()); ttl != "" {
			msg = Message{rewrite(m.Object, ttlFor(ttl))}
		}
		parts := m.Parts()
		hasMarkedPart := false
		for _, p := range parts {
			if markOf(p.ProviderOptions()) != "" {
				hasMarkedPart = true
				break
			}
		}
		if hasMarkedPart {
			np := make([]Part, len(parts))
			for j, p := range parts {
				if ttl := markOf(p.ProviderOptions()); ttl != "" {
					np[j] = Part{rewrite(p.Object, ttlFor(ttl))}
				} else {
					np[j] = p
				}
			}
			msg = msg.Clone()
			msg.SetParts(np)
		}
		out[i] = msg
	}
	if anthropic {
		markLastToolResult(out)
	}
	opts.Prompt = out
	return opts
}

// markLastToolResult puts a short breakpoint on the last tool result, so the
// next step reads everything this step sent.
func markLastToolResult(prompt []Message) {
	if len(prompt) == 0 || prompt[len(prompt)-1].Role() != "tool" {
		return
	}
	last := prompt[len(prompt)-1]
	parts := last.Parts()
	at := -1
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i].Type() == "tool-result" {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	parts[at] = Part{rewrite(parts[at].Object, "5m")}
	c := last.Clone()
	c.SetParts(parts)
	prompt[len(prompt)-1] = c
}
