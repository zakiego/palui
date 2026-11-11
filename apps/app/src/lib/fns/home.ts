// The shell (status, command menu data) and the Home page.
import { ensureWebChat } from "@palui/core/chats"
import { config, masterKey } from "@palui/core/config"
import { getAllSettings, getSetting, setSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { statusSummary } from "@/server/events-stream"
import { firstRunFacts, nextReminders, recentRuns, startOfDay, tokensSince } from "@/server/queries"
import { runtime } from "@/server/runtime"
import { listConnectorItems } from "@/server/connectors"
import { requireUser, requireWriter } from "@/server/session"

export const getShell = createServerFn({ method: "GET" }).handler(async () => {
  const { db, viewer } = await requireUser()
  const settings = getAllSettings(db)
  return {
    viewer,
    summary: statusSummary(db),
    connectors: listConnectorItems({ db, masterKey: safeKey() }).map((c) => ({ id: c.id, label: c.label, status: c.status, enabled: c.enabled, kind: c.kind })),
    uiLanguage: settings.uiLanguage,
    timezone: settings.timezone,
  }
})

function safeKey(): string {
  try {
    return masterKey()
  } catch {
    return ""
  }
}

export const getHome = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const settings = getAllSettings(db)
  const items = listConnectorItems({ db, masterKey: safeKey() })
  const facts = firstRunFacts(db)
  const live = (k: string) => items.some((c) => c.kind === k && (c.status === "on" || c.status === "connecting"))
  const checklist = {
    passkey: facts.passkeys > 0,
    model: live("model"),
    channel: items.some((c) => c.kind === "channel" && c.type !== "web" && (c.status === "on" || c.status === "connecting")),
    hi: settings.firstRun.saidHi || facts.userMessages > 0,
    recall: items.some((c) => c.type === "recall" && c.status !== "not_set_up"),
    mcp: items.some((c) => c.type === "mcp" && c.status !== "not_set_up"),
    dismissed: settings.firstRun.dismissed,
  }
  let runtimeUp = true
  try {
    await runtime().status()
  } catch {
    runtimeUp = false
  }
  return {
    checklist,
    connectors: items.map((c) => ({ id: c.id, type: c.type, kind: c.kind, label: c.label, status: c.status, statusDetail: c.statusDetail, enabled: c.enabled })),
    reminders: nextReminders(db, 3),
    runs: recentRuns(db, 5),
    tokens: tokensSince(db, startOfDay(settings.timezone)),
    timezone: settings.timezone,
    webChatId: ensureWebChat(db).id,
    runtimeUp,
    appUrl: config().appUrl,
  }
})

export const dismissChecklist = createServerFn({ method: "POST" }).handler(async () => {
  const { db } = await requireWriter()
  setSetting(db, "firstRun", { ...getSetting(db, "firstRun"), dismissed: true })
  return { ok: true }
})

export const getStatusSummary = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  return statusSummary(db)
})
