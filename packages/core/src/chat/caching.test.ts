// Prompt caching across real turns (PLAN 5.2): what the model is sent on one
// turn must come back byte for byte at the start of the next.
import { describe, expect, it } from "vitest"
import { memoryDb, runs, type DB } from "@palui/db"
import { getDefaultAgent } from "../agents"
import { agentTokensToday, tokensToday } from "../budget"
import { claimOwner, createClaim } from "../owner"
import { setSetting } from "../settings"
import { TURN_CONTEXT_TAG } from "./context"
import { callTool, channelRuntime, fakeChannel, fnTool, harness, memoryRuntime, modelRuntime, say, scriptedModel, tgMessage, toolsRuntime, type Chunk, type Step } from "./testing"

type Part = { type: string; text?: string; providerOptions?: Record<string, unknown> }
type Message = { role: string; content: string | Part[]; providerOptions?: Record<string, unknown> }

const LONG = { anthropic: { cacheControl: { type: "ephemeral", ttl: "1h" } } }
const SHORT = { anthropic: { cacheControl: { type: "ephemeral" } } }

function ownTelegram(db: DB) {
  const { nonce } = createClaim(db, "telegram", 1)
  claimOwner(db, { channel: "telegram", nonce, externalId: "42" }, 2)
}

// A chat with a clock tool and a memory that recalls something different for every message.
async function chat(steps: Step[], provider?: string) {
  const db = memoryDb()
  ownTelegram(db)
  setSetting(db, "modelRoles", { default: [{ connectorId: "cliproxy", model: "m" }], fast: [{ connectorId: "cliproxy", model: "m" }], vision: [{ connectorId: "cliproxy", model: "m" }] })
  // The model takes a second to answer, so a reply is never stored in the same
  // millisecond as the message it answers: history order must not hang on an id tie.
  const clock = { now: Date.UTC(2026, 10, 10, 6, 0, 0) }
  const timed = steps.map((step): Step => (step instanceof Error ? step : async () => {
    clock.now += 1000
    return typeof step === "function" ? await step() : step
  }))
  const m = scriptedModel(timed, say("ok"), { provider })
  let recalls = 0
  const mem = { autoRecall: true, autoSave: false, recall: async () => `recalled fact ${++recalls}`, save: async () => {} }
  const h = await harness({
    db,
    clock,
    registry: {
      cliproxy: modelRuntime("cliproxy", () => m.model),
      telegram: channelRuntime("telegram", fakeChannel("telegram")),
      clock: toolsRuntime("clock", () => ({ now: { tool: fnTool("What time it is", () => "14.05"), class: "read" } })),
      recall: memoryRuntime(mem as never),
    },
    rows: { cliproxy: "model", telegram: "channel", clock: "tools", recall: "memory" },
  })
  const prompts = () => m.prompts as Message[][]
  return { ...h, m, prompts }
}

const systemOf = (prompt: Message[]) => prompt.filter((p) => p.role === "system")
const messagesOf = (prompt: Message[]) => prompt.filter((p) => p.role !== "system")

// The prompt as the provider hashes it: cache markers are not part of the text.
function bytes(messages: Message[]): string {
  return JSON.stringify(messages, (k, v) => (k === "providerOptions" ? undefined : v))
}

// A message without the per-turn block Palui appends to the newest one.
function withoutTurnContext(m: Message): Message {
  if (typeof m.content === "string") return m
  return { ...m, content: m.content.filter((p) => !(p.type === "text" && p.text?.startsWith(TURN_CONTEXT_TAG))) }
}

describe("prompt caching across turns", () => {
  it("sends a byte-identical prefix on two turns a minute apart", async () => {
    const h = await chat([say("kopi tubruk"), say("soto banjar")], "anthropic.messages")
    await h.engine.inbound(tgMessage("minuman apa yang aku suka"))
    await h.engine.idle()
    h.clock.now += 60_000
    await h.engine.inbound(tgMessage("terus makanan apa yang aku suka"))
    await h.engine.idle()

    const [first, second] = h.prompts()
    expect(first && second).toBeTruthy()

    // The clock and the recall did change between the turns.
    const tail = (p: Message[]) => (p.at(-1)!.content as Part[]).at(-1)!.text!
    expect(tail(first!)).toContain("14.00 WITA")
    expect(tail(second!)).toContain("14.01 WITA")
    expect(tail(first!)).toContain("recalled fact 1")
    expect(tail(second!)).toContain("recalled fact 2")

    // Yet the system prompt is the same, byte for byte, and holds none of it.
    expect(bytes(systemOf(second!))).toBe(bytes(systemOf(first!)))
    expect(bytes(systemOf(first!))).not.toContain("Now:")
    expect(bytes(systemOf(first!))).not.toContain("recalled fact")

    // And the first turn's messages, minus its per-turn block, open the second turn's.
    const sent = messagesOf(first!).map(withoutTurnContext)
    const again = messagesOf(second!).slice(0, sent.length)
    expect(bytes(again)).toBe(bytes(sent))
    // The whole of both requests up to that point: tools, system, messages.
    expect(h.m.offered[1]).toEqual(h.m.offered[0])
  })

  it("sets the breakpoints for the anthropic protocol", async () => {
    const h = await chat([callTool("clock__now", {}), say("jam dua lewat lima")], "anthropic.messages")
    await h.engine.inbound(tgMessage("jam berapa sekarang?"))
    await h.engine.idle()

    const [step1, step2] = h.prompts()
    // Step 1: the stable system block, and the owner's message before the per-turn block.
    expect(systemOf(step1!)[0]!.providerOptions).toEqual(LONG)
    const user = step1!.at(-1)!.content as Part[]
    expect(user.map((p) => p.providerOptions)).toEqual([LONG, undefined])
    expect(user[1]!.text!.startsWith(TURN_CONTEXT_TAG)).toBe(true)
    // Step 2: the same two, plus the tool result the step ends with.
    expect(systemOf(step2!)[0]!.providerOptions).toEqual(LONG)
    expect((step2!.at(-1)!.content as Part[]).at(-1)!.providerOptions).toEqual(SHORT)
    expect(JSON.stringify(step2).split('"cacheControl"').length - 1).toBe(3)
    expect(JSON.stringify(h.prompts())).not.toContain('"palui"')
  })

  it("sets none for the openai protocol, and the turn still answers", async () => {
    const h = await chat([callTool("clock__now", {}), say("jam dua lewat lima")], "cliproxy.chat")
    const res = await h.engine.inbound(tgMessage("jam berapa sekarang?"))
    await h.engine.idle()

    expect(res).not.toBeNull()
    expect(h.prompts()).toHaveLength(2)
    const text = JSON.stringify(h.prompts())
    expect(text).not.toContain("cacheControl")
    expect(text).not.toContain("providerOptions")
    expect(text).not.toContain('"palui"')
    // Same layout, so a gateway that caches by prefix on its own gets the same benefit.
    expect(text).toContain(TURN_CONTEXT_TAG)
  })
})

// A reply whose usage says how much of the input the cache served and stored.
function sayWithCache(text: string, input: { total: number; cacheRead: number; cacheWrite: number }, output: number): Chunk[] {
  const chunks = say(text)
  chunks[chunks.length - 1] = {
    type: "finish",
    finishReason: { unified: "stop", raw: undefined },
    usage: { inputTokens: { ...input, noCache: input.total - input.cacheRead - input.cacheWrite }, outputTokens: { total: output, text: output, reasoning: undefined } },
  }
  return chunks
}

describe("cache tokens on a run", () => {
  it("records cache reads and writes, and counts the reads at a tenth against the global and agent caps", async () => {
    const h = await chat([sayWithCache("halo", { total: 52_000, cacheRead: 40_000, cacheWrite: 2_000 }, 800)], "anthropic.messages")
    await h.engine.inbound(tgMessage("halo"))
    await h.engine.idle()

    const run = h.db.select().from(runs).all()[0]!
    expect(run).toMatchObject({ status: "ok", tokensIn: 52_000, tokensOut: 800, tokensCached: 40_000, tokensCacheWrite: 2_000 })
    // 10,000 fresh + 2,000 written + 4,000 for the 40,000 read + 800 out.
    expect(tokensToday(h.db, h.clock.now)).toBe(16_800)
    expect(agentTokensToday(h.db, getDefaultAgent(h.db).id, h.clock.now)).toBe(16_800)
  })

  it("adds the cache tokens of every step of a turn", async () => {
    const call = callTool("clock__now", {})
    call[call.length - 1] = { ...call.at(-1)!, usage: { inputTokens: { total: 10_000, noCache: 1_000, cacheRead: 0, cacheWrite: 9_000 }, outputTokens: { total: 20, text: 20, reasoning: undefined } } }
    const h = await chat([call, sayWithCache("jam dua", { total: 10_500, cacheRead: 10_000, cacheWrite: 400 }, 30)], "anthropic.messages")
    await h.engine.inbound(tgMessage("jam berapa sekarang?"))
    await h.engine.idle()

    const run = h.db.select().from(runs).all()[0]!
    expect(run).toMatchObject({ steps: 2, tokensIn: 20_500, tokensOut: 50, tokensCached: 10_000, tokensCacheWrite: 9_400 })
    expect(tokensToday(h.db, h.clock.now)).toBe(10_500 + 1_000 + 50)
  })
})
