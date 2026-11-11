// M0 spike (PLAN 14): AI SDK tool calling through CLIProxyAPI.
// Runs N tool-call round trips per model and prints tokens per turn.
//
//   PALUI_SPIKE_KEY=$(cat ~/.config/palui/cliproxy-key) node packages/core/scripts/spike-tool-calling.mjs
//
// From an OrbStack container, set PALUI_SPIKE_BASE=http://host.docker.internal:8317.
// Plain .mjs on purpose: it runs in a bare node:24 container with no build step.
import { createAnthropic } from "@ai-sdk/anthropic"
import { createOpenAICompatible } from "@ai-sdk/openai-compatible"
import { generateText, isStepCount, tool } from "ai"
import { z } from "zod"

const base = process.env.PALUI_SPIKE_BASE || "http://127.0.0.1:8317"
const apiKey = process.env.PALUI_SPIKE_KEY
if (!apiKey) throw new Error("Set PALUI_SPIKE_KEY")
const rounds = Number(process.env.PALUI_SPIKE_ROUNDS || 5)

const anthropic = createAnthropic({ baseURL: `${base}/v1`, apiKey })
const openai = createOpenAICompatible({ name: "cliproxy", baseURL: `${base}/v1`, apiKey })

const targets = [
  { label: "claude-haiku-4-5 (anthropic protocol)", model: anthropic("claude-haiku-4-5-20251001") },
  { label: "claude-sonnet-4-6 (anthropic protocol)", model: anthropic("claude-sonnet-4-6") },
  { label: "claude-haiku-4-5 (openai protocol)", model: openai.chatModel("claude-haiku-4-5-20251001") },
  { label: "gemini-3-flash (openai protocol)", model: openai.chatModel(process.env.PALUI_SPIKE_GEMINI || "gemini-3-flash-preview") },
]

const tools = {
  add: tool({
    description: "Add two integers and return the sum.",
    inputSchema: z.object({ a: z.number().int(), b: z.number().int() }),
    execute: async ({ a, b }) => ({ sum: a + b }),
  }),
}

for (const t of targets) {
  let ok = 0
  const tokens = []
  let lastError = ""
  for (let i = 0; i < rounds; i++) {
    const a = 17 + i * 3
    const b = 25 + i
    try {
      const r = await generateText({
        model: t.model,
        system: "You are a calculator. Always use the add tool, then answer with the number only.",
        prompt: `What is ${a} + ${b}?`,
        tools,
        stopWhen: isStepCount(3),
        maxOutputTokens: 200,
      })
      const usedTool = r.steps.some((s) => s.toolCalls.length > 0)
      if (usedTool && r.text.includes(String(a + b))) ok++
      else lastError = `tool=${usedTool} text=${JSON.stringify(r.text.slice(0, 80))}`
      tokens.push(r.totalUsage.inputTokens + r.totalUsage.outputTokens)
    } catch (e) {
      lastError = String(e?.message ?? e).slice(0, 160)
    }
  }
  const avg = tokens.length ? Math.round(tokens.reduce((x, y) => x + y, 0) / tokens.length) : 0
  console.log(JSON.stringify({ model: t.label, passed: `${ok}/${rounds}`, avgTokensPerTurn: avg, lastError: lastError || undefined }))
}
