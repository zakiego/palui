// Reads manifest zod schemas as form fields. Pure, so the client wizard and the
// server validation share it.
import type { ConnectorManifest, SetupStep } from "@palui/core/connectors/define"
import type { z } from "zod"

type Def = { type?: string; innerType?: z.ZodType; options?: unknown; defaultValue?: unknown; entries?: Record<string, unknown> }
const defOf = (s: z.ZodType) => (s as unknown as { _zod: { def: Def } })._zod.def

export type FieldInfo = {
  kind: "string" | "number" | "boolean" | "enum" | "other"
  options?: string[]
  optional: boolean
  defaultValue?: unknown
}

// What a zod schema looks like to a form. Covers what manifests use: strings,
// numbers, booleans, enums, wrapped in optional/default.
export function describeField(schema: z.ZodType | undefined): FieldInfo {
  let s = schema
  let optional = false
  let defaultValue: unknown
  for (let i = 0; s && i < 5; i++) {
    const d = defOf(s)
    if (d.type === "optional" || d.type === "nullable") {
      optional = true
      s = d.innerType
    } else if (d.type === "default") {
      optional = true
      defaultValue = typeof d.defaultValue === "function" ? (d.defaultValue as () => unknown)() : d.defaultValue
      s = d.innerType
    } else break
  }
  if (!s) return { kind: "other", optional: true }
  const d = defOf(s)
  if (d.type === "string") return { kind: "string", optional, defaultValue }
  if (d.type === "number") return { kind: "number", optional, defaultValue }
  if (d.type === "boolean") return { kind: "boolean", optional, defaultValue }
  if (d.type === "enum") return { kind: "enum", options: Object.keys(d.entries ?? {}), optional, defaultValue }
  return { kind: "other", optional, defaultValue }
}

export function configShape(m: Pick<ConnectorManifest, "config">): Record<string, z.ZodType> {
  const shape = (m.config as unknown as { shape?: Record<string, z.ZodType> }).shape
  return shape ?? {}
}

export type FieldStep = Extract<SetupStep, { field: string }>
export type WizardField = FieldInfo & { secret: boolean }
export type WizardValues = Record<string, string | boolean>

// How one setup step renders in the wizard: secrets are always a write-only text
// box, a zod boolean is a Switch, everything else follows describeField.
export function wizardFieldInfo(m: Pick<ConnectorManifest, "config" | "secrets">, step: FieldStep): WizardField {
  const secretSchema = m.secrets[step.field]
  if (secretSchema) return { ...describeField(secretSchema), kind: "string", secret: true, optional: !!step.optional }
  const info = describeField(configShape(m)[step.field])
  return { ...info, secret: !!step.secret, optional: info.optional || !!step.optional }
}

// The form's starting values from the stored config, falling back to the
// schema default. Booleans are real booleans, secrets start empty.
export function wizardInitialValues(m: Pick<ConnectorManifest, "config" | "secrets" | "setup">, stored: Record<string, unknown>): WizardValues {
  const out: WizardValues = {}
  for (const step of m.setup) {
    if (!("field" in step)) continue
    const info = wizardFieldInfo(m, step)
    if (info.secret) {
      out[step.field] = ""
      continue
    }
    const v = stored[step.field] ?? info.defaultValue
    out[step.field] = info.kind === "boolean" ? v === true : v === undefined || v === null ? "" : String(v)
  }
  return out
}

// The form's values split into config and secrets for saveConnectorDraft. A
// boolean is always sent as true or false, never as an empty string, so
// switching it off is saved instead of clearing the field.
export function wizardPayload(
  m: Pick<ConnectorManifest, "config" | "secrets" | "setup">,
  values: WizardValues,
): { config: Record<string, unknown>; secrets: Record<string, string> } {
  const config: Record<string, unknown> = {}
  const secrets: Record<string, string> = {}
  for (const step of m.setup) {
    if (!("field" in step)) continue
    const info = wizardFieldInfo(m, step)
    const v = values[step.field]
    if (info.secret) {
      // Write-only: an empty box keeps the stored secret.
      if (typeof v === "string" && v.trim()) secrets[step.field] = v.trim()
      continue
    }
    if (info.kind === "boolean") config[step.field] = v === true
    else if (info.kind === "number") config[step.field] = v === "" ? "" : Number(v)
    else config[step.field] = typeof v === "string" ? v.trim() : v
  }
  return { config, secrets }
}
