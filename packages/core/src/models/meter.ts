// Count the tokens of model calls made outside a turn (compaction, reminder
// parsing), so they go toward the daily token cap too (PLAN 5.2).
import type { LanguageModel } from "ai"
import type { DB } from "@palui/db"
import { budgetTokens, recordRunTokens } from "../budget"
import type { ModelV4 } from "./failover"

type ProviderUsage = { inputTokens?: number | { total?: number; cacheRead?: number }; outputTokens?: number | { total?: number } } | undefined

function count(v: number | { total?: number } | undefined): number {
  return (typeof v === "number" ? v : v?.total) ?? 0
}

// Tokens as they count against the daily cap: cache reads at a tenth (budgetTokens).
export function usageTokens(u: ProviderUsage): number {
  const tokensCached = typeof u?.inputTokens === "object" ? u.inputTokens.cacheRead : 0
  return budgetTokens({ tokensIn: count(u?.inputTokens), tokensOut: count(u?.outputTokens), tokensCached })
}

// A model that reports the tokens of every call it serves.
export function meteredModel(model: LanguageModel, onTokens: (tokens: number) => void): LanguageModel {
  if (typeof model === "string" || model.specificationVersion !== "v4") return model
  const m = model as ModelV4
  const report = (u: ProviderUsage) => {
    const n = usageTokens(u)
    if (n > 0) onTokens(n)
  }
  const wrapped = {
    specificationVersion: "v4" as const,
    get provider() {
      return m.provider
    },
    get modelId() {
      return m.modelId
    },
    get supportedUrls() {
      return m.supportedUrls
    },
    async doGenerate(options: Parameters<ModelV4["doGenerate"]>[0]) {
      const r = await m.doGenerate(options)
      report(r.usage as ProviderUsage)
      return r
    },
    async doStream(options: Parameters<ModelV4["doStream"]>[0]) {
      const r = await m.doStream(options)
      const stream = r.stream.pipeThrough(
        new TransformStream({
          transform(part, c) {
            if ((part as { type?: string }).type === "finish") report((part as { usage?: ProviderUsage }).usage)
            c.enqueue(part)
          },
        }),
      )
      return { ...r, stream }
    },
  }
  return wrapped as unknown as LanguageModel
}

// The usual sink: the global daily token counter.
export function meterToBudget(db: DB, model: LanguageModel, clock: () => number = Date.now): LanguageModel {
  return meteredModel(model, (n) => recordRunTokens(db, n, undefined, clock()))
}
