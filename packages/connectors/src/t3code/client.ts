// A small client for the T3 Code server: its OAuth pairing-code sign-in and the
// orchestration tools on its /mcp endpoint. The shapes below are the parts of
// T3 Code 0.1.x's results Palui reads; anything else passes through untouched.
import { createHash, randomBytes } from "node:crypto"
import { McpConnection } from "../mcp/client"

export class T3Error extends Error {
  constructor(
    message: string,
    readonly code: string = "t3_error",
  ) {
    super(message)
    this.name = "T3Error"
  }
}

// A 401 from /mcp: the token ran out or was revoked.
export function isAuthError(err: unknown): boolean {
  const msg = (err as Error)?.message ?? String(err)
  return /\b401\b|unauthori[sz]ed|invalid_mcp_credential/i.test(msg)
}

export function originOf(url: string): string {
  return new URL(url).origin
}

// Kept in config so the same one-time code isn't traded twice.
export function codeFingerprint(code: string): string {
  return createHash("sha256").update(code.trim()).digest("hex").slice(0, 16)
}

// T3 Code's redirect URI must be https or loopback http. Palui never follows it;
// it only reads the code back out of the redirect the decision returns.
const REDIRECT_URI = "http://127.0.0.1/palui-t3code"

export type PairResult = { token: string; expiresAt: number | null }

// Trade a pairing code (t3 auth pairing create) for a full-access MCP token:
// register a client, approve with the code, exchange the authorization code.
// T3 Code ties the token to the origin it was asked at, so `url` must be the
// address the runtime will keep using.
export async function pairWithCode(url: string, pairingCode: string, opts: { fetch?: typeof fetch; now?: number } = {}): Promise<PairResult> {
  const f = opts.fetch ?? fetch
  const origin = originOf(url)
  const resource = `${origin}/mcp`
  const json = async (res: Response, step: string) => {
    const text = await res.text()
    let body: Record<string, unknown> = {}
    try {
      body = text ? (JSON.parse(text) as Record<string, unknown>) : {}
    } catch {
      // Not JSON: fall through with the raw text in the error.
    }
    if (!res.ok) {
      const why = (body.message ?? body.error_description ?? body.error ?? text.slice(0, 200)) || `HTTP ${res.status}`
      throw new T3Error(`T3 Code refused the ${step}: ${String(why)}`, "pairing_rejected")
    }
    return body
  }
  const post = (path: string, body: unknown) => f(`${origin}${path}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) })

  const reg = await json(await post("/oauth/mcp/register", { client_name: "Palui", redirect_uris: [REDIRECT_URI] }), "registration")
  const clientId = String(reg.client_id ?? "")
  if (!clientId) throw new T3Error("T3 Code registered Palui without a client id. Update T3 Code.", "incompatible")

  const verifier = randomBytes(32).toString("base64url")
  const challenge = createHash("sha256").update(verifier).digest("base64url")
  const state = randomBytes(16).toString("base64url")
  const decided = await json(
    await post("/oauth/mcp/decision", {
      authorization: {
        response_type: "code",
        client_id: clientId,
        redirect_uri: REDIRECT_URI,
        code_challenge: challenge,
        code_challenge_method: "S256",
        state,
        resource,
      },
      decision: { _tag: "pairing-code", access: "full-access", code: pairingCode.trim() },
    }),
    "pairing code",
  )
  const redirect = URL.parse(String(decided.redirectTo ?? ""))
  const code = redirect?.searchParams.get("code")
  if (!redirect || !code || redirect.searchParams.get("state") !== state) {
    const why = redirect?.searchParams.get("error_description") ?? "Check the code and make a new one if it's older than its TTL."
    throw new T3Error(`T3 Code didn't accept the pairing code. ${why}`, "pairing_rejected")
  }

  const tok = await json(
    await f(`${origin}/oauth/mcp/token`, {
      method: "POST",
      headers: { "content-type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ grant_type: "authorization_code", code, redirect_uri: REDIRECT_URI, client_id: clientId, code_verifier: verifier, resource }),
    }),
    "token exchange",
  )
  const token = String(tok.access_token ?? "")
  if (!token) throw new T3Error("T3 Code answered the token exchange without a token. Update T3 Code.", "incompatible")
  const expiresIn = Number(tok.expires_in)
  return { token, expiresAt: Number.isFinite(expiresIn) && expiresIn > 0 ? (opts.now ?? Date.now()) + expiresIn * 1000 : null }
}

// --- Tool results ---------------------------------------------------------------------

export type T3Project = {
  id: string
  title: string
  workspaceRoot: string
  defaultModelSelection?: unknown
  archivedAt?: string | null
  deletedAt?: string | null
}

export type T3Model = { instanceId: string; model: string }

type Capabilities = {
  providers: { providerInstanceId: string; models: { id: string }[]; constraints?: string[] }[]
}

// "claudeAgent/claude-sonnet-5-5" -> { instanceId, model }. Null when malformed.
export function parseModel(value: string | undefined): T3Model | null {
  const m = /^([^/\s]+)\/(\S+)$/.exec(value?.trim() ?? "")
  return m ? { instanceId: m[1]!, model: m[2]! } : null
}

export type T3ThreadSummary = {
  threadId: string
  projectId: string | null
  title: string
  link?: string
  status: string
  latestRunId?: string | null
  activeRunId?: string | null
  branch?: string | null
  pendingRequestCount?: number
  pullRequests?: { url?: string; number?: number; state?: string }[]
  updatedAt?: string
}

export type T3Item = { itemId: string; runId?: string | null; type: string; text?: string | null; textTruncated?: boolean; position: number }

export type T3ThreadRead = { thread: T3ThreadSummary; items: T3Item[]; recentRuns?: { runId: string; status: string }[] }

export type T3Launch = { threadId: string; projectId: string; runId: string | null; link?: string }

export type T3Wait = { threadId: string; runId?: string | null; status: string; timedOut: boolean }

export class T3Client {
  readonly conn: McpConnection

  constructor(url: string, token: string, fetchImpl?: typeof fetch) {
    this.conn = new McpConnection({
      url: `${originOf(url)}/mcp`,
      headers: { Authorization: `Bearer ${token}` },
      clientName: "palui",
      legacyProtocol: true,
      ...(fetchImpl ? { fetch: fetchImpl } : {}),
    })
  }

  close(): Promise<void> {
    return this.conn.close()
  }

  // One tool call. A tool-level failure (isError) becomes a T3Error with the
  // server's message, which is written for an agent and safe to show the model.
  async call<T>(tool: string, args: Record<string, unknown>): Promise<T> {
    const res = await this.conn.callTool(tool, args)
    const content = (res as { content?: { type: string; text?: string }[] }).content ?? []
    const text = content.find((c) => typeof c.text === "string")?.text ?? ""
    if ((res as { isError?: boolean }).isError) {
      let message = text || `${tool} failed`
      let code = "t3_error"
      try {
        const parsed = JSON.parse(text) as { message?: string; code?: string }
        if (parsed.message) message = parsed.message
        if (parsed.code) code = parsed.code
      } catch {
        // Plain text failure.
      }
      throw new T3Error(message, code)
    }
    const structured = (res as { structuredContent?: unknown }).structuredContent
    if (structured !== undefined) return structured as T
    try {
      return JSON.parse(text) as T
    } catch {
      throw new T3Error(`T3 Code sent an unexpected ${tool} result. Update T3 Code or Palui.`, "incompatible")
    }
  }

  async projects(): Promise<T3Project[]> {
    const out: T3Project[] = []
    let cursor: number | null = 0
    for (let page = 0; page < 10 && cursor !== null; page++) {
      const res: { projects: T3Project[]; nextCursor: number | null } = await this.call("t3_project_list", { cursor, limit: 100 })
      out.push(...res.projects)
      cursor = res.nextCursor
    }
    return out.filter((p) => !p.archivedAt && !p.deletedAt)
  }

  threads(input: { projectId?: string; limit: number; statuses?: string[] }): Promise<{ threads: T3ThreadSummary[] }> {
    return this.call("t3_thread_list", {
      ...(input.projectId ? { projectId: input.projectId } : { allProjects: true }),
      ...(input.statuses?.length ? { statuses: input.statuses } : {}),
      includeSubagents: false,
      limit: input.limit,
    })
  }

  // The first usable provider's first model. A client outside any thread has no
  // model to inherit, and T3 Code refuses a launch without one unless the
  // project sets a default.
  async defaultModel(): Promise<T3Model> {
    const caps: Capabilities = await this.call("orchestrator_capabilities", {})
    const p = caps.providers.find((x) => x.models.length > 0 && !(x.constraints?.length ?? 0))
    if (!p) throw new T3Error("T3 Code has no provider with a model turned on. Enable one in T3 Code's settings.", "no_model")
    return { instanceId: p.providerInstanceId, model: p.models[0]!.id }
  }

  launch(input: { projectId: string; title: string; message: string; baseRef?: string; model?: T3Model; clientRequestId: string }): Promise<T3Launch> {
    return this.call("t3_thread_launch", {
      projectId: input.projectId,
      ...(input.model ? { modelSelection: input.model } : {}),
      title: input.title,
      message: input.message,
      clientRequestId: input.clientRequestId,
      ...(input.baseRef ? { workspaceStrategy: { type: "worktree", baseRef: input.baseRef } } : {}),
    })
  }

  send(input: { threadId: string; message: string; clientRequestId: string }): Promise<Record<string, unknown>> {
    return this.call("t3_thread_send", { threadId: input.threadId, message: input.message, mode: "auto", clientRequestId: input.clientRequestId })
  }

  read(input: { threadId: string; limit: number; maxCharsPerItem: number }): Promise<T3ThreadRead> {
    return this.call("t3_thread_read", { threadId: input.threadId, view: "messages", limit: input.limit, runLimit: 3, maxCharsPerItem: input.maxCharsPerItem })
  }

  // Returns at once with timedOut when the run is still going.
  peek(threadId: string, runId: string | null): Promise<T3Wait> {
    return this.call("t3_thread_wait", { threadId, ...(runId ? { runId } : {}), timeoutMs: 1 })
  }

  interrupt(input: { threadId: string; clientRequestId: string }): Promise<Record<string, unknown>> {
    return this.call("t3_thread_interrupt", { threadId: input.threadId, reason: "Stopped from Palui", clientRequestId: input.clientRequestId })
  }
}

// Pick a project from what the model said: an id, a name, a repo name, or a path.
export function matchProject(projects: T3Project[], query: string): { project: T3Project } | { error: string } {
  const q = query.trim().toLowerCase()
  if (!q) return { error: "Name a project." }
  const base = (p: T3Project) => p.workspaceRoot.replace(/\/+$/, "").split("/").pop()?.toLowerCase() ?? ""
  const exact = projects.filter((p) => p.id.toLowerCase() === q || p.title.toLowerCase() === q || base(p) === q || p.workspaceRoot.toLowerCase() === q)
  if (exact.length === 1) return { project: exact[0]! }
  const loose = exact.length ? exact : projects.filter((p) => p.title.toLowerCase().includes(q) || base(p).includes(q))
  if (loose.length === 1) return { project: loose[0]! }
  const names = (loose.length ? loose : projects).slice(0, 15).map((p) => p.title)
  if (loose.length > 1) return { error: `"${query}" matches ${loose.length} projects: ${names.join(", ")}. Ask which one.` }
  return { error: `No T3 Code project matches "${query}". Projects: ${names.join(", ")}.` }
}
