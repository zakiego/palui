// Connector presets: a manifest's prefilled configs (the MCP server's Vorneo,
// Linear...). The Add connector page lists them apart from the custom setup.
import type { ConnectorManifest } from "@palui/core/connectors/define"

export type Preset = NonNullable<ConnectorManifest["presets"]>[number]

export function presetFor(m: ConnectorManifest | undefined, id: string | undefined): Preset | undefined {
  return id ? m?.presets?.find((p) => p.id === id) : undefined
}

// The host a preset points at (vorneo.id), so it says where it connects.
export function presetHost(p: Preset): string | null {
  const url = p.config?.url
  if (typeof url !== "string") return null
  try {
    return new URL(url).host
  } catch {
    return null
  }
}
