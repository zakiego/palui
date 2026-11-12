// Validation for the settings the owner can change, shared by the dashboard's
// Settings page and the agent's own settings tools so both accept the same values.
import { z } from "zod"
import { REASONING_EFFORTS } from "./models/effort"

export function isTimeZone(tz: string): boolean {
  try {
    new Intl.DateTimeFormat("en", { timeZone: tz })
    return true
  } catch {
    return false
  }
}

export const timezoneSchema = z.string().min(1).max(64).refine(isTimeZone, "Use an IANA zone like Asia/Makassar.")

export const modelChainSchema = z
  .array(z.object({ connectorId: z.string().min(1).max(64), model: z.string().trim().min(1).max(120), effort: z.enum(REASONING_EFFORTS).optional() }))
  .min(1)
  .max(6)

export const budgetsSchema = z.object({
  maxSteps: z.number().int().min(1).max(50),
  turnTimeoutMs: z.number().int().min(10_000).max(600_000),
  maxTools: z.number().int().min(1).max(128),
  // 0 turns the global cap off, the same as an agent's or a job's cap.
  dailyTokenCap: z.union([z.literal(0), z.number().int().min(10_000).max(100_000_000)]),
  maxRunsPerHour: z.number().int().min(1).max(1000),
})

export const languageSchema = z.enum(["auto", "id", "en"])
export const uiLanguageSchema = z.enum(["en", "id"])
export const fallbackChannelSchema = z.enum(["telegram", "whatsapp", "none"])

// A patch of the Settings page keys. Every key is optional; objects are whole.
export const settingsPatchSchema = z.object({
  timezone: timezoneSchema.optional(),
  language: languageSchema.optional(),
  uiLanguage: uiLanguageSchema.optional(),
  modelRoles: z.object({ default: modelChainSchema, fast: modelChainSchema, vision: modelChainSchema }).optional(),
  budgets: budgetsSchema.optional(),
  fallbackChannel: fallbackChannelSchema.optional(),
})

export type SettingsPatch = z.infer<typeof settingsPatchSchema>
