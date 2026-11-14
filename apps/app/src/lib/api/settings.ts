// Settings page.
import { noInput, withInput } from "./client"
import type { ModelChainLink } from "@/shared/constants"
import type { ChatOption, ConnectorStatus, HookView, Ok, PasskeyItem } from "./types"

export type ModelRoles = { default: ModelChainLink[]; fast: ModelChainLink[]; vision: ModelChainLink[] }

export type Budgets = { maxSteps: number; turnTimeoutMs: number; maxTools: number; dailyTokenCap: number; maxRunsPerHour: number }

export type Language = "auto" | "en" | "id"
export type UILanguage = "en" | "id"
export type FallbackChannel = "none" | "telegram" | "whatsapp"

export type SettingsValues = {
  timezone: string
  language: Language
  uiLanguage: UILanguage
  modelRoles: ModelRoles
  budgets: Budgets
  fallbackChannel: FallbackChannel
}

export type SettingsPage = {
  settings: SettingsValues
  // icon is left out when the connector's type has no manifest.
  models: { id: string; label: string; status: ConnectorStatus; icon?: string }[]
  passkeys: PasskeyItem[]
  backup: { file: string; bytes: number; at: number } | null
  hooks: HookView[]
  chats: ChatOption[]
  appUrl: string
}

export type SettingsPatch = Partial<SettingsValues>

export const getSettingsPage = noInput<SettingsPage>("getSettingsPage", "GET")
// The model ids a running model connector offers, for the model picker.
export const listConnectorModels = withInput<{ id: string }, { models: string[] }>("listConnectorModels", "GET")
export const saveSettings = withInput<SettingsPatch, Ok>("saveSettings", "POST")
export const createHookFn = withInput<{ name: string; chatId: string; mode: "forward" | "summarize" }, { slug: string; url: string; secret: string }>(
  "createHookFn",
  "POST",
)
