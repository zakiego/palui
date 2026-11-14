// The connector manifests the dashboard renders (cards, wizards, the catalog),
// from src/shared/connectors (the dashboard's copy of the Go manifests).
import { manifestList } from "@/shared/connectors/manifests"
import type { ConnectorManifest } from "@/shared/connectors/define"

export const manifests: ConnectorManifest[] = manifestList

export function manifestFor(type: string): ConnectorManifest | undefined {
  return manifests.find((m) => m.id === type)
}
