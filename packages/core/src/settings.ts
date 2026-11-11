// Typed settings with defaults, stored as JSON in the `settings` table.
import { eq } from "drizzle-orm"
import { settings, type DB } from "@palui/db"

export type ModelRole = "default" | "fast" | "vision"

export type Settings = {
  persona: string
  timezone: string
  // Bot language: "auto" = casual Indonesian, English when the owner writes English (D6).
  language: "auto" | "id" | "en"
  uiLanguage: "en" | "id"
  // Per role, an ordered failover chain of { connectorId, model }.
  modelRoles: Record<ModelRole, { connectorId: string; model: string }[]>
  budgets: {
    maxSteps: number
    turnTimeoutMs: number
    maxTools: number
    dailyTokenCap: number
    maxRunsPerHour: number
  }
  // Channel used when the primary one fails twice (PLAN 6) or is removed (4.3).
  fallbackChannel: "telegram" | "whatsapp" | "none"
  reminderFallbackAfterAttempts: number
  // Paused by a hit budget cap until this time (ms), or null.
  turnsPausedUntil: number | null
  turnsPausedReason: string | null
  firstRun: { saidHi: boolean; dismissed: boolean }
}

export const DEFAULT_PERSONA = `Kamu Palui, asisten pribadi Zaki. Gayamu santai, singkat, dan to the point, seperti teman yang cerdas.
Default bahasa Indonesia santai. Kalau Zaki menulis dalam bahasa Inggris, jawab dalam bahasa Inggris.
Jangan mengarang fakta. Kalau tidak tahu, bilang tidak tahu. Pakai tools kalau memang membantu.
Kalau sebuah tool tidak disetujui, jangan coba lagi tanpa ditanya.`

export const DEFAULT_SETTINGS: Settings = {
  persona: DEFAULT_PERSONA,
  timezone: "Asia/Makassar",
  language: "auto",
  uiLanguage: "en",
  modelRoles: {
    default: [
      { connectorId: "cliproxy", model: "claude-sonnet-4-6" },
      { connectorId: "ai-gateway", model: "claude-sonnet-4-6" },
    ],
    fast: [
      { connectorId: "cliproxy", model: "claude-haiku-4-5-20251001" },
      { connectorId: "ai-gateway", model: "claude-haiku-4-5-20251001" },
    ],
    vision: [
      { connectorId: "cliproxy", model: "claude-sonnet-4-6" },
      { connectorId: "ai-gateway", model: "claude-sonnet-4-6" },
    ],
  },
  budgets: {
    maxSteps: 12,
    turnTimeoutMs: 120_000,
    maxTools: 30,
    dailyTokenCap: 2_000_000,
    maxRunsPerHour: 60,
  },
  fallbackChannel: "telegram",
  reminderFallbackAfterAttempts: 2,
  turnsPausedUntil: null,
  turnsPausedReason: null,
  firstRun: { saidHi: false, dismissed: false },
}

export function getSetting<K extends keyof Settings>(db: DB, key: K): Settings[K] {
  const row = db.select().from(settings).where(eq(settings.key, key)).get()
  if (!row) return DEFAULT_SETTINGS[key]
  const def = DEFAULT_SETTINGS[key]
  // Merge objects so a new default field shows up on old rows.
  if (def && typeof def === "object" && !Array.isArray(def) && row.value && typeof row.value === "object") {
    return { ...def, ...(row.value as object) } as Settings[K]
  }
  return row.value as Settings[K]
}

export function setSetting<K extends keyof Settings>(db: DB, key: K, value: Settings[K], now = Date.now()) {
  db.insert(settings)
    .values({ key, value, updatedAt: now })
    .onConflictDoUpdate({ target: settings.key, set: { value, updatedAt: now } })
    .run()
}

export function getAllSettings(db: DB): Settings {
  const out = { ...DEFAULT_SETTINGS }
  for (const key of Object.keys(DEFAULT_SETTINGS) as (keyof Settings)[]) {
    ;(out as Record<string, unknown>)[key] = getSetting(db, key)
  }
  return out
}
