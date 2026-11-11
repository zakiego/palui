// The control API contract between the app and the runtime (PLAN 3.1). The
// runtime serves it on the compose network only (never published). Every call
// carries the derived control token in `x-palui-control`.
import type { TestResult } from "./connectors/define"

export const CONTROL_HEADER = "x-palui-control"

export type ConnectorLive = {
  id: string
  running: boolean
  status: string
  detail: string | null
  live: Record<string, unknown>
}

export type RuntimeStatus = {
  ok: boolean
  startedAt: number
  schema: string
  connectors: ConnectorLive[]
  turnsPaused: { until: number | null; reason: string | null }
  activeTurns: string[]
}

// Events streamed to the web chat (and any dashboard transcript) over SSE.
export type TurnEvent =
  | { type: "turn-start"; chatId: string; runId: string }
  | { type: "text-delta"; chatId: string; runId: string; delta: string }
  | { type: "tool-call"; chatId: string; runId: string; toolCallId: string; tool: string; class: string; args: unknown }
  | { type: "tool-result"; chatId: string; runId: string; toolCallId: string; tool: string; preview: string; ms: number; status: "ok" | "error" | "denied" }
  | { type: "approval"; chatId: string; runId: string; approvalId: string; tool: string; class: string; argsPreview: string }
  | { type: "message"; chatId: string; messageId: string; role: "user" | "assistant"; text: string }
  | { type: "turn-end"; chatId: string; runId: string; status: string; error?: string }

export type ParsedReminder = {
  ok: boolean
  text: string
  // ISO with offset in the reminder zone.
  fireAtIso?: string
  fireAt?: number
  rrule?: string | null
  tz: string
  // "Besok, Rabu 11 Nov, 09.00 WITA"
  echo?: string
  error?: string
}

export type ControlRoutes = {
  "GET /live": { res: { ok: boolean; schema: string } }
  "GET /control/status": { res: RuntimeStatus }
  "POST /control/reconcile": { res: { ok: true } }
  "POST /control/test/:id": { res: TestResult }
  "POST /control/action/:id": { body: { name: string; input?: Record<string, unknown> }; res: Record<string, unknown> }
  "POST /control/remove/:id": { res: { ok: boolean; detail?: string } }
  "POST /control/web/send": { body: { text: string }; res: { ok: true; chatId: string; messageId: string } }
  "GET /control/stream": { res: "text/event-stream of TurnEvent" }
  "POST /control/approvals/:id": { body: { approved: boolean }; res: { ok: boolean } }
  "POST /control/jobs/:id/run": { res: { ok: boolean; runId?: string; detail?: string } }
  "POST /control/stop/:chatId": { res: { ok: boolean } }
  "POST /control/reminders/parse": { body: { text: string; tz?: string }; res: ParsedReminder }
}

export class ControlError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message)
  }
}

// Used by the app (server side only). Short timeouts: the dashboard must never
// hang on a busy or restarting runtime.
export function controlClient(baseUrl: string, token: string, fetchImpl: typeof fetch = fetch) {
  async function call<T>(method: "GET" | "POST", path: string, body?: unknown, timeoutMs = 15_000): Promise<T> {
    let res: Response
    try {
      res = await fetchImpl(`${baseUrl}${path}`, {
        method,
        headers: { [CONTROL_HEADER]: token, ...(body !== undefined ? { "content-type": "application/json" } : {}) },
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal: AbortSignal.timeout(timeoutMs),
      })
    } catch (e) {
      throw new ControlError(`The runtime isn't reachable (${(e as Error).message}). Check docker compose logs runtime.`, 503)
    }
    const text = await res.text()
    let data: unknown = null
    try {
      data = text ? JSON.parse(text) : null
    } catch {
      data = { error: text }
    }
    if (!res.ok) throw new ControlError((data as { error?: string })?.error ?? `runtime answered ${res.status}`, res.status)
    return data as T
  }
  return {
    status: () => call<RuntimeStatus>("GET", "/control/status", undefined, 3_000),
    reconcile: () => call<{ ok: true }>("POST", "/control/reconcile", {}, 5_000),
    test: (id: string) => call<TestResult>("POST", `/control/test/${encodeURIComponent(id)}`, {}, 30_000),
    action: (id: string, name: string, input: Record<string, unknown> = {}) =>
      call<Record<string, unknown>>("POST", `/control/action/${encodeURIComponent(id)}`, { name, input }, 30_000),
    remove: (id: string) => call<{ ok: boolean; detail?: string }>("POST", `/control/remove/${encodeURIComponent(id)}`, {}, 30_000),
    webSend: (text: string) => call<{ ok: true; chatId: string; messageId: string }>("POST", "/control/web/send", { text }),
    approve: (id: string, approved: boolean) => call<{ ok: boolean }>("POST", `/control/approvals/${encodeURIComponent(id)}`, { approved }),
    runJob: (id: string) => call<{ ok: boolean; runId?: string; detail?: string }>("POST", `/control/jobs/${encodeURIComponent(id)}/run`, {}),
    stop: (chatId: string) => call<{ ok: boolean }>("POST", `/control/stop/${encodeURIComponent(chatId)}`, {}),
    // Switch a chat's agent (PLAN 19.2): saves the session to the old agent's memory first. null means Palui.
    setChatAgent: (chatId: string, agentId: string | null) => call<{ ok: true }>("POST", `/control/chats/${encodeURIComponent(chatId)}/agent`, { agentId }, 20_000),
    parseReminder: (text: string, tz?: string) => call<ParsedReminder>("POST", "/control/reminders/parse", { text, tz }, 20_000),
    // Raw SSE stream for the app to proxy to the browser.
    stream: (signal?: AbortSignal) => fetchImpl(`${baseUrl}/control/stream`, { headers: { [CONTROL_HEADER]: token }, signal }),
  }
}

export type ControlClient = ReturnType<typeof controlClient>
