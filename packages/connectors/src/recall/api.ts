// A small client for recall's REST API (/health, /v1/*). Shapes follow
// recall's apps/app/src/server/api.ts.
import { createHash } from "node:crypto"

export class RecallError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = "RecallError"
  }
}

export type RecallContainer = { container: string; memories: number; documents: number; lastActivity: number | null }

export type RecallMemory = { id: string; fact: string; kind?: "static" | "dynamic" }

export type RecallProfile = {
  container: string
  static: string
  dynamic: string
  updatedAt: number | null
  staticMemories: RecallMemory[]
  dynamicMemories: RecallMemory[]
  searchResults?: { type: string; id: string; text: string; score?: number }[]
}

export type RecallAddResult = { id: string; container: string; status: string; sourceId: string | null; duplicate: boolean }

// recall caps source_id at 200 characters. Longer ids are hashed so they stay unique and stable.
export const MAX_SOURCE_ID = 200
export function normalizeSourceId(id: string): string {
  const trimmed = id.trim()
  if (trimmed.length <= MAX_SOURCE_ID) return trimmed
  return `palui:${createHash("sha256").update(trimmed).digest("hex")}`
}

export class RecallApi {
  readonly base: string

  constructor(
    url: string,
    private readonly apiKey: string,
    private readonly fetchImpl: typeof fetch = fetch,
  ) {
    this.base = url.replace(/\/+$/, "")
  }

  get mcpUrl(): string {
    return `${this.base}/mcp`
  }

  private async call<T>(method: string, path: string, opts: { body?: unknown; signal?: AbortSignal; auth?: boolean } = {}): Promise<T> {
    const headers: Record<string, string> = { accept: "application/json" }
    if (opts.auth !== false) headers.authorization = `Bearer ${this.apiKey}`
    if (opts.body !== undefined) headers["content-type"] = "application/json"
    let res: Response
    try {
      res = await this.fetchImpl(`${this.base}${path}`, {
        method,
        headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        signal: opts.signal,
      })
    } catch (err) {
      if ((err as Error)?.name === "AbortError" || (err as Error)?.name === "TimeoutError") throw err
      throw new RecallError(0, `Can't reach recall at ${this.base}: ${(err as Error)?.message ?? String(err)}`)
    }
    const text = await res.text()
    let data: unknown = null
    try {
      data = text ? JSON.parse(text) : null
    } catch {
      data = null
    }
    if (!res.ok) {
      const err = (data as { error?: { message?: string } | string } | null)?.error
      const message = typeof err === "string" ? err : (err?.message ?? (text.slice(0, 200) || res.statusText))
      if (res.status === 401) throw new RecallError(401, "recall rejected the API key (401)")
      throw new RecallError(res.status, `recall answered ${res.status}: ${message}`)
    }
    return data as T
  }

  health(signal?: AbortSignal) {
    return this.call<{ ok: boolean; [k: string]: unknown }>("GET", "/health", { auth: false, signal })
  }

  async containers(signal?: AbortSignal): Promise<RecallContainer[]> {
    const res = await this.call<{ containers: RecallContainer[] }>("GET", "/v1/containers", { signal })
    return res.containers ?? []
  }

  profile(container: string, q: string | undefined, signal?: AbortSignal) {
    const params = new URLSearchParams({ container })
    if (q) params.set("q", q.slice(0, 2000))
    return this.call<RecallProfile>("GET", `/v1/profile?${params}`, { signal })
  }

  search(container: string, q: string, limit = 10, signal?: AbortSignal) {
    return this.call<{ container: string; q: string; results: { type: string; id: string; text: string; score: number }[] }>("POST", "/v1/search", {
      body: { container, q, limit },
      signal,
    })
  }

  addMemory(input: { container: string; content: string; title?: string; sourceId?: string }, signal?: AbortSignal) {
    return this.call<RecallAddResult>("POST", "/v1/memories", {
      body: {
        container: input.container,
        content: input.content,
        ...(input.title ? { title: input.title.slice(0, 300) } : {}),
        ...(input.sourceId ? { source_id: normalizeSourceId(input.sourceId) } : {}),
      },
      signal,
    })
  }

  getMemory(id: string, signal?: AbortSignal) {
    return this.call<{ memory: RecallMemory & { container: string } }>("GET", `/v1/memories/${encodeURIComponent(id)}`, { signal })
  }

  forget(id: string, signal?: AbortSignal) {
    return this.call<{ memory: RecallMemory }>("DELETE", `/v1/memories/${encodeURIComponent(id)}`, { signal })
  }

  getDocument(id: string, signal?: AbortSignal) {
    return this.call<{ document: { id: string; container: string; title?: string | null } }>("GET", `/v1/documents/${encodeURIComponent(id)}`, { signal })
  }

  // Deletes the stored text and its chunks. Facts already taken from it stay
  // until they're forgotten one by one.
  deleteDocument(id: string, signal?: AbortSignal) {
    return this.call<{ deleted: boolean }>("DELETE", `/v1/documents/${encodeURIComponent(id)}`, { signal })
  }
}

const MAX_PROFILE_CHARS = 4000

// Profile text for the prompt, or null when recall knows nothing useful.
export function profileText(p: RecallProfile, about = "the owner"): string | null {
  const parts: string[] = []
  const stat = p.static?.trim()
  const dyn = p.dynamic?.trim()
  if (stat) parts.push(`About ${about}: ${stat}`)
  else if (p.staticMemories?.length) parts.push(`Facts about ${about}:\n${p.staticMemories.slice(0, 10).map((m) => `- ${m.fact}`).join("\n")}`)
  if (dyn) parts.push(`Lately: ${dyn}`)
  else if (p.dynamicMemories?.length) parts.push(`Recent facts:\n${p.dynamicMemories.slice(0, 10).map((m) => `- ${m.fact}`).join("\n")}`)
  const hits = (p.searchResults ?? []).map((r) => r.text?.trim()).filter(Boolean).slice(0, 8)
  if (hits.length) parts.push(`Memories related to this message:\n${hits.map((h) => `- ${h}`).join("\n")}`)
  if (!parts.length) return null
  const text = parts.join("\n\n")
  return text.length > MAX_PROFILE_CHARS ? `${text.slice(0, MAX_PROFILE_CHARS - 3)}...` : text
}
