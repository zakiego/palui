// A tiny i18n layer: flat keys, two locales, `{name}` placeholders. The locale
// comes from settings.uiLanguage (set in the app shell); pages outside the
// shell (login, invite) use English.
import * as React from "react"
import { en, type Key } from "./en"
import { id } from "./id"

export type Locale = "en" | "id"
export type { Key }

const TABLES: Record<Locale, Record<Key, string>> = { en, id }

export function translate(locale: Locale, key: Key, vars?: Record<string, string | number>): string {
  const text = TABLES[locale][key] ?? en[key]
  if (!vars) return text
  return text.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m))
}

const LocaleContext = React.createContext<Locale>("en")

export function LocaleProvider({ locale, children }: { locale: Locale; children: React.ReactNode }) {
  return <LocaleContext.Provider value={locale}>{children}</LocaleContext.Provider>
}

export function useT() {
  const locale = React.useContext(LocaleContext)
  return React.useCallback((key: Key, vars?: Record<string, string | number>) => translate(locale, key, vars), [locale])
}

export function useLocale() {
  return React.useContext(LocaleContext)
}
