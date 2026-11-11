// Entry points that start flow runs without waiting for the timer (RFC flows
// 4.1, 7.1): a verified webhook in hook mode `flow`, and a `chat` trigger the
// router matched before the model. Each inserts a queued run (wake_at = now,
// keyed so a redelivery or a repeat is a no-op) and pokes the scheduler.
import { createHash } from "node:crypto"
import { and, eq } from "drizzle-orm"
import { flowTriggers, hooks, type DB } from "@palui/db"
import { logEvent } from "../events"
import type { HookRow } from "../hooks/index"
import { normalizeAnswer } from "../routines/service"
import type { ChatTrigger } from "./schema"
import { currentVersion, definitionOf, getFlow } from "./service"
import { startFlowRun } from "./start"

// Headers a webhook flow can read (trigger.headers). Everything else is dropped.
export const HOOK_HEADERS = ["content-type", "user-agent", "x-palui-delivery", "x-github-event", "x-github-delivery", "x-gitlab-event", "x-event-key"]

export function hookPayload(raw: string): unknown {
  try {
    return JSON.parse(raw)
  } catch {
    return { text: raw.slice(0, 8000) }
  }
}

// The run key of a delivery: the sender's delivery id when it sends one
// (x-palui-delivery, or GitHub's X-GitHub-Delivery), else a hash of the body.
export function deliveryKey(raw: string, headers: Record<string, string | null | undefined>): string {
  const id = headers["x-palui-delivery"] ?? headers["x-github-delivery"]
  if (id && /^[\w.:-]{1,120}$/.test(id)) return `hook:${id}`
  return `hook:${createHash("sha256").update(raw).digest("hex").slice(0, 40)}`
}

// A verified request to a hook in mode `flow`: one run per flow whose webhook
// trigger names this hook. The whole payload is untrusted (trigger is tainted).
export function ingestFlowHook(db: DB, hook: HookRow, raw: string, headers: Record<string, string | null | undefined>, now = Date.now()): { started: number; skipped: number; listeners: number } {
  const rows = db
    .select()
    .from(flowTriggers)
    .where(and(eq(flowTriggers.kind, "webhook"), eq(flowTriggers.match, hook.id)))
    .all()
  const kept = Object.fromEntries(HOOK_HEADERS.flatMap((h) => (headers[h] ? [[h, String(headers[h]).slice(0, 200)]] : [])))
  const payload = { kind: "webhook", payload: hookPayload(raw), headers: kept, receivedAt: now, hook: hook.name }
  const key = deliveryKey(raw, headers)
  let started = 0
  let skipped = 0
  for (const row of rows) {
    const r = startFlowRun(db, { flowId: row.flowId, triggerId: row.triggerId, triggerKind: "webhook", runKey: key, payload, taint: ["trigger"], now })
    if (r.run && !r.skipped) started++
    else skipped++
  }
  db.update(hooks).set({ lastAt: now }).where(eq(hooks.id, hook.id)).run()
  if (!rows.length) logEvent(db, { level: "warn", kind: "hook.unrouted", message: `Hook "${hook.name}" is in flow mode but no flow listens to it.`, data: { hookId: hook.id } }, now)
  return { started, skipped, listeners: rows.length }
}

// Chat triggers: the owner's message in the flow's chat that is the command
// ("/beres", with the rest as args) or one of the phrases, normalized like a
// check-in answer. True when a flow took it (the message never reaches a turn).
export function matchChatTrigger(db: DB, input: { chatId: string; text: string; messageId: string; fromOwner: boolean }, now = Date.now()): boolean {
  if (!input.fromOwner) return false
  const text = input.text.trim()
  if (!text) return false
  const rows = db.select().from(flowTriggers).where(eq(flowTriggers.kind, "chat")).all()
  let hit = false
  for (const row of rows) {
    const flow = getFlow(db, row.flowId)
    if (!flow || flow.chatId !== input.chatId) continue
    const t = definitionOf(currentVersion(db, flow)).triggers.find((x): x is ChatTrigger => x.id === row.triggerId && x.kind === "chat")
    if (!t) continue
    let args: string | null = null
    if (t.command) {
      const [head, ...rest] = text.split(/\s+/)
      if (head!.toLowerCase().replace(/@\S+$/, "") === t.command) args = rest.join(" ")
    } else if (t.phrases?.some((p) => normalizeAnswer(p) === normalizeAnswer(text))) args = ""
    if (args === null) continue
    startFlowRun(db, { flowId: flow.id, triggerId: t.id, triggerKind: "chat", runKey: `chat:${input.messageId}`, payload: { kind: "chat", text: text.slice(0, 2000), args: args.slice(0, 2000), messageId: input.messageId }, now })
    hit = true
  }
  return hit
}

// Saving a flow with a webhook trigger puts that hook in mode `flow` (RFC 4.1).
export function claimHooksForFlow(db: DB, hookIds: string[], now = Date.now()): string[] {
  const changed: string[] = []
  for (const id of hookIds) {
    const h = db.select().from(hooks).where(eq(hooks.id, id)).get()
    if (!h || h.mode === "flow") continue
    db.update(hooks).set({ mode: "flow" }).where(eq(hooks.id, id)).run()
    logEvent(db, { kind: "hook.mode", message: `Hook "${h.name}" now starts flows instead of ${h.mode === "forward" ? "forwarding" : "summarizing"} its events.`, data: { hookId: id } }, now)
    changed.push(h.name)
  }
  return changed
}
