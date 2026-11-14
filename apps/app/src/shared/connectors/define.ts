// The manifest half of the connector contract, the dashboard's side of
// apps/server/internal/connectors/define.go. The runtime half (start, test,
// tools) lives in the Go server. ../manifest-golden.test.ts checks every
// manifest against the golden the Go registry tests read too.
import type { z } from "zod"

export type ConnectorKind = "channel" | "model" | "memory" | "tools" | "skill"
export type Trust = "own" | "external"

// --- Manifest ------------------------------------------------------------------------

export type SetupStep =
  | {
      field: string
      label: string
      help?: string
      // A command or value the help refers to, shown as a copyable block between
      // the help and the input (guide first, then the box to paste into).
      // `{appUrl}` is replaced with the dashboard's URL.
      command?: string
      secret?: boolean
      placeholder?: string
      optional?: boolean
      // Before the first connect the page shows only required fields. This
      // shows an optional one there too (MCP's auth mode and token).
      askFirst?: boolean
      // Show the field only while another field has one of these values.
      showIf?: { field: string; in: string[] }
    }
  // Wizard actions the dashboard renders specially.
  | { action: "claimOwner" | "pairWhatsApp" | "ownerPhone" | "pickContainer" | "listTools" | "oauth"; label: string; help?: string }

export type ConnectorManifest<C extends z.ZodType = z.ZodType, S extends Record<string, z.ZodType> = Record<string, z.ZodType>> = {
  // Manifest/type id ("telegram", "mcp", "cliproxy"...).
  id: string
  kind: ConnectorKind
  name: string
  // lucide icon name, rendered by the dashboard.
  icon: string
  summary: string
  // Longer explanation for the sheet and the generated docs page.
  description?: string
  config: C
  secrets: S
  setup: SetupStep[]
  // Several instances allowed (MCP servers). Instance ids become "<id>-<slug>".
  multiple?: boolean
  // Default trust for tool results from this connector.
  trust?: Trust
  // Created (paused or on) on first boot without asking: skills like Reminders.
  builtin?: boolean
  // Text shown on the card about what pause does (PLAN 4.3, e.g. Reminders).
  pauseNote?: string
  // Text for the remove dialog ("This unlinks the WhatsApp device...").
  removeWarning?: string
  // Remove calls the running handle's action("unlink") first (WhatsApp logs the
  // linked device out). Connectors without it are just stopped.
  unlinkOnRemove?: boolean
  // When a running handle reports `error` (after start() resolved), the
  // supervisor stops it and restarts it on the backoff ladder (PLAN 4.3).
  // Default: true for channels, false otherwise (tool and memory connectors
  // report soft errors and recover on their own). A connector that reconnects
  // by itself and whose error states are final sets false.
  restartOnError?: boolean
  // Presets for multiple-instance connectors (MCP: vorneo, keuangan...).
  presets?: { id: string; label: string; config: Record<string, unknown>; secretFields?: string[]; trust?: Trust }[]
  docs?: { commands?: string[]; notes?: string[] }
}

export function defineManifest<C extends z.ZodType, S extends Record<string, z.ZodType>>(m: ConnectorManifest<C, S>): ConnectorManifest<C, S> {
  return m
}

export type ConfigOf<M> = M extends ConnectorManifest<infer C, infer _S> ? z.infer<C> : never
export type SecretsOf<M> = M extends ConnectorManifest<infer _C, infer S> ? { [K in keyof S]?: z.infer<S[K]> } : never
