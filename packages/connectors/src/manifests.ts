// Every connector manifest, keyed by manifest id. The app and the runtime both
// import this, so it must stay free of runtime-only code.
import type { ConnectorManifest } from "@palui/core/connectors/define"
import attachments from "./attachments/manifest"
import chatHistory from "./chats/manifest"
import aiGateway from "./ai-gateway/manifest"
import cliproxy from "./cliproxy/manifest"
import gcal from "./gcal/manifest"
import hooks from "./hooks/manifest"
import jobs from "./jobs/manifest"
import mcp from "./mcp/manifest"
import notes from "./notes/manifest"
import recall from "./recall/manifest"
import reminders from "./reminders/manifest"
import routines from "./routines/manifest"
import remoteMcp from "./remote-mcp/manifest"
import t3code from "./t3code/manifest"
import telegram from "./telegram/manifest"
import tgbot from "./tgbot/manifest"
import voice from "./voice/manifest"
import web from "./web/manifest"
import webfetch from "./webfetch/manifest"
import whatsapp from "./whatsapp/manifest"

// Order is the catalog and Connectors page order within each kind.
export const manifests: Record<string, ConnectorManifest> = {
  telegram,
  tgbot,
  whatsapp,
  web,
  cliproxy,
  "ai-gateway": aiGateway,
  recall,
  mcp,
  gcal,
  t3code,
  reminders,
  routines,
  jobs,
  chats: chatHistory,
  notes,
  "remote-mcp": remoteMcp,
  attachments,
  voice,
  webfetch,
  hooks,
}

export const manifestList: ConnectorManifest[] = Object.values(manifests)

export function getManifest(id: string): ConnectorManifest | undefined {
  return manifests[id]
}
