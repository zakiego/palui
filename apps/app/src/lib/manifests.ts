// The connector manifests the dashboard renders (cards, wizards, the catalog),
// from the shared registry in packages/connectors.
import { manifestList } from "@palui/connectors/manifests"
import type { ConnectorManifest } from "@palui/core/connectors/define"

export const manifests: ConnectorManifest[] = manifestList

export function manifestFor(type: string): ConnectorManifest | undefined {
  return manifests.find((m) => m.id === type)
}
