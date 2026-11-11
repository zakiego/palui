import { describe, expect, it } from "vitest"
import { cacheMark, speaksAnthropic, withPromptCache } from "./cache"

type Options = Parameters<typeof withPromptCache>[0]

const LONG = { type: "ephemeral", ttl: "1h" }
const SHORT = { type: "ephemeral" }

function options(prompt: unknown[]): Options {
  return { prompt } as unknown as Options
}

// A turn's first step: marked system block, summary, history, newest user message with the per-turn block.
function firstStep() {
  return [
    { role: "system", content: "stable", providerOptions: cacheMark("1h") },
    { role: "system", content: "summary" },
    { role: "user", content: [{ type: "text", text: "halo" }] },
    { role: "assistant", content: [{ type: "text", text: "hai" }] },
    {
      role: "user",
      content: [
        { type: "text", text: "jam berapa?", providerOptions: cacheMark("1h") },
        { type: "text", text: "[Palui context]\nNow: 14.05" },
      ],
    },
  ]
}

function secondStep() {
  return [
    ...firstStep(),
    { role: "assistant", content: [{ type: "tool-call", toolCallId: "c1", toolName: "clock__now", input: {} }] },
    {
      role: "tool",
      content: [
        { type: "tool-result", toolCallId: "c1", toolName: "clock__now", output: { type: "text", value: "14.05" } },
        { type: "tool-result", toolCallId: "c2", toolName: "clock__now", output: { type: "text", value: "14.05" } },
      ],
    },
  ]
}

const breakpoints = (o: Options) => JSON.stringify(o.prompt).split('"cacheControl"').length - 1

describe("withPromptCache", () => {
  it("knows the anthropic protocol by its provider name", () => {
    expect(speaksAnthropic("anthropic.messages")).toBe(true)
    expect(speaksAnthropic("anthropic")).toBe(true)
    expect(speaksAnthropic("cliproxy.chat")).toBe(false)
    expect(speaksAnthropic("anthropicish")).toBe(false)
  })

  it("turns the marks into anthropic breakpoints: system block and newest user message, both for an hour", () => {
    const out = withPromptCache(options(firstStep()), "anthropic.messages")
    const prompt = out.prompt as unknown as { content: unknown; providerOptions?: unknown }[]
    expect(prompt[0]!.providerOptions).toEqual({ anthropic: { cacheControl: LONG } })
    expect(prompt[1]!.providerOptions).toBeUndefined()
    expect(prompt[4]!.content).toEqual([
      { type: "text", text: "jam berapa?", providerOptions: { anthropic: { cacheControl: LONG } } },
      { type: "text", text: "[Palui context]\nNow: 14.05" },
    ])
    expect(breakpoints(out)).toBe(2)
    expect(JSON.stringify(out.prompt)).not.toContain("palui")
  })

  it("adds a short breakpoint on the last tool result from the second step on", () => {
    const out = withPromptCache(options(secondStep()), "anthropic.messages")
    const tool = out.prompt.at(-1) as unknown as { content: { providerOptions?: unknown }[] }
    expect(tool.content[0]!.providerOptions).toBeUndefined()
    expect(tool.content[1]!.providerOptions).toEqual({ anthropic: { cacheControl: SHORT } })
    // Three in all, under Anthropic's limit of four, the hour-long ones first.
    expect(breakpoints(out)).toBe(3)
  })

  it("skips approval responses when it looks for the last tool result", () => {
    const prompt = [...secondStep(), { role: "tool", content: [{ type: "tool-result", toolCallId: "c3", toolName: "t", output: { type: "text", value: "x" } }, { type: "tool-approval-response", approvalId: "a", approved: true }] }]
    const tool = withPromptCache(options(prompt), "anthropic.messages").prompt.at(-1) as unknown as { content: { providerOptions?: unknown }[] }
    expect(tool.content[0]!.providerOptions).toEqual({ anthropic: { cacheControl: SHORT } })
    expect(tool.content[1]!.providerOptions).toBeUndefined()
  })

  it("keeps other provider options next to the breakpoint", () => {
    const prompt = [{ role: "system", content: "s", providerOptions: { ...cacheMark("5m"), anthropic: { foo: 1 }, other: { bar: 2 } } }]
    expect(withPromptCache(options(prompt), "anthropic.messages").prompt[0]!.providerOptions).toEqual({ anthropic: { foo: 1, cacheControl: SHORT }, other: { bar: 2 } })
  })

  it("sends the openai protocol nothing: no breakpoints, no marks", () => {
    for (const step of [firstStep(), secondStep()]) {
      const out = withPromptCache(options(step), "cliproxy.chat")
      const text = JSON.stringify(out.prompt)
      expect(text).not.toContain("cacheControl")
      expect(text).not.toContain("anthropic")
      expect(text).not.toContain("palui")
      // The same prompt a turn built before caching existed.
      expect(out.prompt).toEqual(JSON.parse(JSON.stringify(step, (k, v) => (k === "providerOptions" ? undefined : v))))
    }
  })

  it("leaves a prompt without marks alone, whatever the protocol", () => {
    const o = options([{ role: "system", content: "s" }, { role: "user", content: [{ type: "text", text: "x" }] }, { role: "tool", content: [{ type: "tool-result", toolCallId: "c", toolName: "t", output: { type: "text", value: "x" } }] }])
    expect(withPromptCache(o, "anthropic.messages")).toBe(o)
    expect(withPromptCache(o, "cliproxy.chat")).toBe(o)
  })

  it("does not change the options it was given", () => {
    const o = options(secondStep())
    const before = JSON.stringify(o)
    withPromptCache(o, "anthropic.messages")
    expect(JSON.stringify(o)).toBe(before)
  })
})
