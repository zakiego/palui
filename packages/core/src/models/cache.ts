// Prompt caching (PLAN 5.2). A provider can reuse a prompt it has already read,
// but only up to a marked point and only while everything before that point is
// byte for byte the same. The turn sets provider-neutral marks on the points
// worth keeping; here, per model link, they become Anthropic cache breakpoints
// (`providerOptions.anthropic.cacheControl`) or are dropped for every other
// protocol, so an OpenAI-style request carries nothing new.
//
// Three breakpoints at most (Anthropic allows four):
//   1. the stable system block, which also covers the tool list before it (1h)
//   2. the newest user message, before the per-turn block (1h)
//   3. the last tool result, from the second step of a turn on (5m)
//
// Why two lifetimes. A cached prefix costs 1.25x to write for 5 minutes and 2x
// for an hour; reading it costs 0.1x and restarts its clock. Chat messages
// often come more than 5 minutes apart, so 1 and 2 would expire between most
// turns on the short lifetime and the whole prompt would be written again; on
// the long one the extra 0.75x is paid once on what a turn adds. Breakpoint 3
// is only read by the next step of the same turn, seconds later, and can't be
// reused by the next turn (history replays tool results as short previews), so
// it stays on the cheap lifetime. Longer lifetimes must come first in a
// request, which this order satisfies.
import type { ModelV4 } from "./failover"

export type CacheTtl = "5m" | "1h"

// providerOptions key of the neutral mark.
export const CACHE_MARK = "palui"

type Options = Record<string, Record<string, unknown>>

// Mark a system message or a message part as the end of a reusable prefix.
export function cacheMark(ttl: CacheTtl): { [CACHE_MARK]: { cache: CacheTtl } } {
  return { [CACHE_MARK]: { cache: ttl } }
}

export function speaksAnthropic(provider: string): boolean {
  return provider === "anthropic" || provider.startsWith("anthropic.")
}

type CallOptions = Parameters<ModelV4["doGenerate"]>[0]
type Prompt = CallOptions["prompt"]
type Carrier = { providerOptions?: Options }

function markOf(c: Carrier): CacheTtl | undefined {
  const v = c.providerOptions?.[CACHE_MARK]?.cache
  return v === "5m" || v === "1h" ? v : undefined
}

function cacheControl(ttl: CacheTtl) {
  return ttl === "1h" ? { type: "ephemeral", ttl } : { type: "ephemeral" }
}

// The carrier without the neutral mark, and with a breakpoint when `ttl` is set.
function rewrite<T extends Carrier>(c: T, ttl: CacheTtl | undefined): T {
  const { [CACHE_MARK]: _mark, ...rest } = c.providerOptions ?? {}
  const providerOptions: Options = ttl ? { ...rest, anthropic: { ...rest.anthropic, cacheControl: cacheControl(ttl) } } : rest
  const { providerOptions: _old, ...bare } = c
  return (Object.keys(providerOptions).length ? { ...bare, providerOptions } : bare) as T
}

// Call options for one model link. A prompt without marks (compaction, reminder
// parsing, the connector test) is returned as it came.
export function withPromptCache(options: CallOptions, provider: string): CallOptions {
  const prompt = options.prompt
  const marked = prompt.some((m) => markOf(m as Carrier) || (Array.isArray(m.content) && m.content.some((p) => markOf(p as Carrier))))
  if (!marked) return options
  const anthropic = speaksAnthropic(provider)
  const out = prompt.map((m) => {
    const ttl = markOf(m as Carrier)
    const message = ttl ? rewrite(m as Carrier, anthropic ? ttl : undefined) : m
    if (!Array.isArray(m.content) || !m.content.some((p) => markOf(p as Carrier))) return message
    return { ...message, content: m.content.map((p) => (markOf(p as Carrier) ? rewrite(p as Carrier, anthropic ? markOf(p as Carrier) : undefined) : p)) }
  }) as Prompt
  if (anthropic) markLastToolResult(out)
  return { ...options, prompt: out }
}

// From the second step on the prompt ends with tool results. A breakpoint on
// the last one lets the next step read everything this step sent.
function markLastToolResult(prompt: Prompt): void {
  const last = prompt.at(-1)
  if (last?.role !== "tool") return
  const at = last.content.findLastIndex((p) => p.type === "tool-result")
  if (at < 0) return
  const content = last.content.slice()
  content[at] = rewrite(content[at] as Carrier, "5m") as (typeof content)[number]
  prompt[prompt.length - 1] = { ...last, content }
}
