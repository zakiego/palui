// Dev only: exactly one of everything. This is the pluralization test ("1
// runs", "1 need attention") and the "list that looks broken with one row" test.
import { H, MIN, type Kit } from "./kit"

export function one(k: Kit) {
  const { now } = k

  // One connector, and it's the one that needs attention.
  k.connector("telegram", "telegram", { status: "error", detail: "401 Unauthorized" })

  const web = k.webChat()
  k.message(web, "user", "hi", now - MIN)

  k.setting("modelRoles", {
    default: [{ connectorId: "cliproxy", model: "claude-sonnet-4-6" }],
    fast: [{ connectorId: "cliproxy", model: "claude-haiku-4-5-20251001" }],
    vision: [{ connectorId: "cliproxy", model: "claude-sonnet-4-6" }],
  })

  const jobId = k.job({ name: "Daily briefing", kind: "briefing", rrule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0", tz: "Asia/Makassar", prompt: "Brief me.", chatId: web.id, nextRunAt: now + H, lastRunAt: now - MIN, lastStatus: "ok", lastResult: "1 reminder today.", dailyTokenCap: 1000 })
  // One run today, one token in, one millisecond.
  k.run({ chatId: web.id, jobId, trigger: "schedule", model: "claude-sonnet-4-6", tokensIn: 1, tokensOut: 0, steps: 1, status: "ok", startedAt: now - MIN, ms: 1 })

  k.reminder({ chatId: web.id, text: "Bayar listrik", tz: "Asia/Makassar", fireAt: now + H, status: "pending" })
  k.reminder({ chatId: web.id, text: "Standup", rrule: "FREQ=DAILY", tz: "Asia/Makassar", fireAt: now + 2 * H, status: "pending" })
  k.reminder({ chatId: web.id, text: "Old one", tz: "Asia/Makassar", fireAt: now - H, status: "done", lastFiredAt: now - H })

  const flow = k.flow(web, { name: "Air", triggers: [{ id: "main", kind: "schedule", start: "say", source: { kind: "times", times: [{ time: "11:00" }] } }], steps: [{ id: "say", kind: "message", text: "Minum air" }] })
  k.flowRun(flow, { status: "done", triggerId: "main", startedAt: now - MIN, steps: [{ stepId: "say", kind: "message", status: "ok", output: { text: "Minum air" } }] })

  k.passkey({ name: "MacBook Air", createdAt: now - 24 * H })
  k.hook({ name: "Uptime", chatId: web.id, lastAt: now - H })
}
