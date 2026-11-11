// Model failover per role (PLAN 5.2). settings.modelRoles[role] is an ordered
// chain of { connectorId, model }. A failover model tries each running link in
// order and moves on when a call fails with 5xx, 429, a quota error or a network
// error. Errors after a stream has started are not retried here (the turn may
// already have run tools).
import { APICallError, type LanguageModel } from "ai"
import type { DB } from "@palui/db"
import { getSetting, type ModelRole } from "../settings"
import type { RunningConnector } from "../saklar/define"

export type ModelV4 = Extract<LanguageModel, { specificationVersion: "v4" }>

export type ModelLink = { connectorId: string; model: string; lm: ModelV4 }

// What the failover model needs from the supervisor.
export type ModelHost = {
  get(id: string): { handle: RunningConnector } | undefined
}

export function resolveChain(db: DB, host: ModelHost, role: ModelRole): ModelLink[] {
  const roles = getSetting(db, "modelRoles")
  const chain = roles[role]?.length ? roles[role] : roles.default
  const out: ModelLink[] = []
  for (const link of chain ?? []) {
    const entry = host.get(link.connectorId)
    if (!entry?.handle.model) continue
    try {
      const lm = entry.handle.model(link.model, role)
      if (typeof lm === "string" || lm.specificationVersion !== "v4") continue
      out.push({ connectorId: link.connectorId, model: link.model, lm })
    } catch {
      // A connector that can't build the model is skipped like a paused one.
    }
  }
  return out
}

export function isFailoverError(e: unknown): boolean {
  if (!e) return false
  if (APICallError.isInstance(e)) {
    const s = e.statusCode
    if (s === 429 || (s !== undefined && s >= 500)) return true
    if (s === 402 || s === 403) return /quota|credit|billing|limit|exhaust/i.test(`${e.message} ${e.responseBody ?? ""}`)
    if (s === undefined) return true // no response: network failure
    return false
  }
  const err = e as { name?: string; message?: string; code?: string; cause?: unknown }
  if (err.name === "AbortError" || err.name === "TimeoutError") return false
  const text = `${err.message ?? ""} ${err.code ?? ""}`
  if (/quota|rate.?limit|overloaded|too many requests|insufficient|ECONNREFUSED|ECONNRESET|ENOTFOUND|EAI_AGAIN|fetch failed|socket hang up/i.test(text)) return true
  if (err.cause && err.cause !== e) return isFailoverError(err.cause)
  return false
}

export type FailoverModel = ModelV4 & {
  // The link that served the last successful call.
  served(): ModelLink | undefined
  links: ModelLink[]
}

// A LanguageModel that delegates to the first link that answers.
export function failoverModel(links: ModelLink[], onFailover?: (from: ModelLink, error: unknown) => void): FailoverModel {
  if (!links.length) throw new Error("No model connector is running")
  let last: ModelLink | undefined
  const first = links[0]!.lm
  async function attempt<T>(fn: (lm: ModelV4) => PromiseLike<T>): Promise<T> {
    let lastError: unknown
    for (let i = 0; i < links.length; i++) {
      const link = links[i]!
      try {
        const r = await fn(link.lm)
        last = link
        return r
      } catch (e) {
        lastError = e
        if (i === links.length - 1 || !isFailoverError(e)) throw e
        onFailover?.(link, e)
      }
    }
    throw lastError
  }
  const model = {
    specificationVersion: "v4" as const,
    get provider() {
      return (last ?? links[0]!).lm.provider
    },
    get modelId() {
      return (last ?? links[0]!).model
    },
    supportedUrls: first.supportedUrls,
    doGenerate: (options: Parameters<ModelV4["doGenerate"]>[0]) => attempt((lm) => lm.doGenerate(options)),
    doStream: (options: Parameters<ModelV4["doStream"]>[0]) => attempt((lm) => lm.doStream(options)),
    served: () => last,
    links,
  }
  return model as unknown as FailoverModel
}
