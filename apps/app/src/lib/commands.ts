// Custom command names and the form's own checks, mirroring internal/customcmd.
import type { Key } from "@/lib/i18n"

export const NAME_RE = /^[a-z0-9][a-z0-9_-]{0,31}$/
export const NAME_MAX = 32
export const DESCRIPTION_MAX = 120
export const PROMPT_MAX = 4000

// Lowercase, trimmed, without a leading slash.
export function normalizeName(name: string) {
  return name.trim().toLowerCase().replace(/^\//, "")
}

export type CommandErrors = { name?: { key: Key; name?: string }; prompt?: { key: Key } }

export function checkCommand(form: { name: string; prompt: string }, builtins: string[]): CommandErrors {
  const errors: CommandErrors = {}
  const name = normalizeName(form.name)
  if (!name) errors.name = { key: "commands.err.nameEmpty" }
  else if (!NAME_RE.test(name)) errors.name = { key: "commands.err.name" }
  else if (builtins.includes(`/${name}`)) errors.name = { key: "commands.err.builtin", name: `/${name}` }
  if (!form.prompt.trim()) errors.prompt = { key: "commands.err.prompt" }
  return errors
}
