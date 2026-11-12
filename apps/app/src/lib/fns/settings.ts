// Settings page.
import { config, masterKey } from "@palui/core/config"
import { getAllSettings, setSetting, type Settings } from "@palui/core/settings"
import { settingsPatchSchema } from "@palui/core/settings-schema"
import { listConnectors } from "@palui/core/connectors/store"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { manifestFor } from "@/lib/manifests"
import { createHook, listHooks } from "@/server/hooks-bridge"
import { callRuntime } from "@/server/runtime"
import { chatOptions, latestBackup, passkeyList } from "@/server/queries"
import { requireUser, requireWriter } from "@/server/session"

export const getSettingsPage = createServerFn({ method: "GET" }).handler(async () => {
  const { db, viewer } = await requireUser()
  const s = getAllSettings(db)
  return {
    settings: {
      timezone: s.timezone,
      language: s.language,
      uiLanguage: s.uiLanguage,
      modelRoles: s.modelRoles,
      budgets: s.budgets,
      fallbackChannel: s.fallbackChannel,
      deployNotice: s.deployNotice,
    },
    models: listConnectors(db)
      .filter((c) => c.kind === "model")
      .map((c) => ({ id: c.id, label: c.label, status: c.status, icon: manifestFor(c.type)?.icon })),
    passkeys: passkeyList(db, viewer.user.id),
    backup: latestBackup(config().dataDir),
    hooks: listHooks(db),
    chats: chatOptions(db),
    appUrl: config().appUrl,
  }
})

// The model ids a running model connector offers, for the model picker. A
// paused connector or a down runtime answers with an error the picker shows.
export const listConnectorModels = createServerFn({ method: "GET" })
  .validator(z.object({ id: z.string().min(1).max(64) }))
  .handler(async ({ data }) => {
    await requireUser()
    const res = await callRuntime((rt) => rt.action(data.id, "listModels", {}))
    const models = Array.isArray(res.models) ? res.models.filter((m): m is string => typeof m === "string") : []
    return { models: [...new Set(models)].sort((a, b) => a.localeCompare(b)) }
  })

export const saveSettings = createServerFn({ method: "POST" })
  .validator(settingsPatchSchema)
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    for (const [key, value] of Object.entries(data) as [keyof Settings, never][]) {
      if (value !== undefined) setSetting(db, key, value)
    }
    return { ok: true }
  })

export const createHookFn = createServerFn({ method: "POST" })
  .validator(z.object({ name: z.string().trim().min(1).max(60), chatId: z.string().min(1).max(100), mode: z.enum(["forward", "summarize"]) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const { hook, secret } = createHook(db, data, masterKey())
    return { slug: hook.slug, url: `${config().appUrl}/api/hooks/${hook.slug}`, secret }
  })
