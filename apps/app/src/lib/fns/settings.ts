// Settings page.
import { config, masterKey } from "@palui/core/config"
import { getAllSettings, setSetting, type Settings } from "@palui/core/settings"
import { listConnectors } from "@palui/core/connectors/store"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { createHook, listHooks } from "@/server/hooks-bridge"
import { chatOptions, latestBackup, passkeyList } from "@/server/queries"
import { requireUser, requireWriter } from "@/server/session"

export const getSettingsPage = createServerFn({ method: "GET" }).handler(async () => {
  const { db, viewer } = await requireUser()
  const s = getAllSettings(db)
  return {
    settings: {
      persona: s.persona,
      timezone: s.timezone,
      language: s.language,
      uiLanguage: s.uiLanguage,
      modelRoles: s.modelRoles,
      budgets: s.budgets,
      fallbackChannel: s.fallbackChannel,
    },
    models: listConnectors(db)
      .filter((c) => c.kind === "model")
      .map((c) => ({ id: c.id, label: c.label, status: c.status })),
    passkeys: passkeyList(db, viewer.user.id),
    backup: latestBackup(config().dataDir),
    hooks: listHooks(db),
    chats: chatOptions(db),
    appUrl: config().appUrl,
  }
})

const chain = z.array(z.object({ connectorId: z.string().min(1).max(64), model: z.string().trim().min(1).max(120) })).min(1).max(6)

const patch = z.object({
  persona: z.string().trim().min(1).max(8000).optional(),
  timezone: z
    .string()
    .min(1)
    .max(64)
    .refine((tz) => {
      try {
        new Intl.DateTimeFormat("en", { timeZone: tz })
        return true
      } catch {
        return false
      }
    }, "Use an IANA zone like Asia/Makassar.")
    .optional(),
  language: z.enum(["auto", "id", "en"]).optional(),
  uiLanguage: z.enum(["en", "id"]).optional(),
  modelRoles: z.object({ default: chain, fast: chain, vision: chain }).optional(),
  budgets: z
    .object({
      maxSteps: z.number().int().min(1).max(50),
      turnTimeoutMs: z.number().int().min(10_000).max(600_000),
      maxTools: z.number().int().min(1).max(128),
      dailyTokenCap: z.number().int().min(10_000).max(100_000_000),
      maxRunsPerHour: z.number().int().min(1).max(1000),
    })
    .optional(),
  fallbackChannel: z.enum(["telegram", "whatsapp", "none"]).optional(),
})

export const saveSettings = createServerFn({ method: "POST" })
  .validator(patch)
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
