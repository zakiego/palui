// Live reminder parse eval (PLAN 6, 17). Runs every phrase in phrases.ts through
// parseReminder against a fixed "now" and scores exact matches.
//
//   pnpm --filter @palui/core eval:reminders
//
// Env: PALUI_EVAL_BASE (default http://127.0.0.1:8317), PALUI_EVAL_KEY (default:
// the contents of ~/.config/palui/cliproxy-key), PALUI_EVAL_MODEL (default
// claude-haiku-4-5-20251001), PALUI_EVAL_ONLY (substring filter on the phrase).
// Exits 1 below 95%.
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { createAnthropic } from "@ai-sdk/anthropic"
import { parseReminder } from "../parse"
import { localToUtc, normalizeRrule, occurrencesAfter, utcToLocal } from "../time"
import { EVAL_NOW_LOCAL, EVAL_TZ, PHRASES, type EvalPhrase } from "./phrases"

const PASS_RATE = 0.95
const CONCURRENCY = 4

function readKey(): string {
  const fromEnv = process.env.PALUI_EVAL_KEY
  if (fromEnv) return fromEnv.trim()
  const file = path.join(os.homedir(), ".config", "palui", "cliproxy-key")
  try {
    return fs.readFileSync(file, "utf8").trim()
  } catch {
    throw new Error(`Set PALUI_EVAL_KEY or put the CLIProxyAPI key in ${file}.`)
  }
}

function safeNormalize(rule: string | null | undefined): string | null {
  if (!rule) return null
  try {
    return normalizeRrule(rule)
  } catch {
    return `INVALID(${rule})`
  }
}

// Same fire instant, and the same rule: equal normalized bodies, or (for rules
// written differently, like FREQ=DAILY;BYDAY=MO,... vs FREQ=WEEKLY;BYDAY=MO,...)
// the same next 8 occurrences.
export function matches(p: EvalPhrase, got: { fireAt?: number; rrule?: string | null }, now: number): { ok: boolean; why?: string } {
  const expectedAt = localToUtc(p.localDateTime, EVAL_TZ)
  if (got.fireAt !== expectedAt) return { ok: false, why: "time" }
  const a = safeNormalize(p.rrule)
  const b = safeNormalize(got.rrule)
  if (a === b) return { ok: true }
  if (!a || !b || b.startsWith("INVALID")) return { ok: false, why: "rrule" }
  try {
    const ea = occurrencesAfter({ rrule: a, tz: EVAL_TZ, localStart: p.localDateTime }, now, 8)
    const eb = occurrencesAfter({ rrule: b, tz: EVAL_TZ, localStart: utcToLocal(got.fireAt, EVAL_TZ) }, now, 8)
    if (ea.length === eb.length && ea.every((t, i) => t === eb[i])) return { ok: true }
  } catch {
    // fall through
  }
  return { ok: false, why: "rrule" }
}

async function pool<T, R>(items: T[], n: number, fn: (item: T, i: number) => Promise<R>): Promise<R[]> {
  const out: R[] = new Array(items.length)
  let next = 0
  await Promise.all(
    Array.from({ length: Math.min(n, items.length) }, async () => {
      while (next < items.length) {
        const i = next++
        out[i] = await fn(items[i] as T, i)
      }
    }),
  )
  return out
}

async function main() {
  const base = process.env.PALUI_EVAL_BASE || "http://127.0.0.1:8317"
  const modelId = process.env.PALUI_EVAL_MODEL || "claude-haiku-4-5-20251001"
  const only = process.env.PALUI_EVAL_ONLY
  const anthropic = createAnthropic({ baseURL: `${base}/v1`, apiKey: readKey() })
  const model = anthropic(modelId)
  const now = localToUtc(EVAL_NOW_LOCAL, EVAL_TZ)
  const phrases = only ? PHRASES.filter((p) => p.text.includes(only)) : PHRASES

  const started = Date.now()
  const results = await pool(phrases, CONCURRENCY, async (p) => {
    const got = await parseReminder(p.text, EVAL_TZ, now, model, { timeoutMs: 30_000, maxRetries: 2 })
    return { p, got, verdict: got.ok ? matches(p, got, now) : { ok: false, why: `error: ${got.error}` } }
  })

  const lines: string[] = []
  lines.push(`Reminder parse eval: model ${modelId}, now ${EVAL_NOW_LOCAL} ${EVAL_TZ} (Tuesday), ${phrases.length} phrases`)
  lines.push("")
  const failures = results.filter((r) => !r.verdict.ok)
  for (const { p, got, verdict } of failures) {
    const gotLocal = got.fireAt !== undefined ? utcToLocal(got.fireAt, EVAL_TZ).slice(0, 16) : "-"
    lines.push(`FAIL [${p.lang}] ${p.text}`)
    lines.push(`  expected ${p.localDateTime} ${p.rrule ?? "one-off"}`)
    lines.push(`  got      ${gotLocal} ${got.rrule ?? (got.ok ? "one-off" : "")} (${verdict.why})`)
  }
  const passed = results.length - failures.length
  const byLang = (lang: "id" | "en") => {
    const rs = results.filter((r) => r.p.lang === lang)
    return `${rs.filter((r) => r.verdict.ok).length}/${rs.length}`
  }
  if (failures.length) lines.push("")
  lines.push(`Indonesian ${byLang("id")}, English ${byLang("en")}`)
  const rate = results.length ? passed / results.length : 0
  lines.push(`Score: ${passed}/${results.length} (${(rate * 100).toFixed(1)}%), pass mark ${PASS_RATE * 100}%, ${((Date.now() - started) / 1000).toFixed(1)} s`)
  console.log(lines.join("\n"))
  process.exit(rate >= PASS_RATE ? 0 : 1)
}

main().catch((e) => {
  console.error(`eval crashed: ${(e as Error).message}`)
  process.exit(2)
})
