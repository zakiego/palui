// Every connector manifest, keyed by manifest id. The app and the runtime both
// import this, so it must stay free of runtime-only code.
import type { ConnectorManifest } from "./define"
import attachments from "./attachments/manifest"
import chatHistory from "./chats/manifest"
import cliproxy from "./cliproxy/manifest"
import gateway from "./gateway/manifest"
import gcal from "./gcal/manifest"
import hooks from "./hooks/manifest"
import http from "./http/manifest"
import ics from "./ics/manifest"
import jobs from "./jobs/manifest"
import proactive from "./proactive/manifest"
import mcp from "./mcp/manifest"
import outbound from "./outbound/manifest"
import printer from "./printer/manifest"
import notes from "./notes/manifest"
import settings from "./settings/manifest"
import recall from "./recall/manifest"
import reminders from "./reminders/manifest"
import watches from "./watches/manifest"
import flows from "./flows/manifest"
import remoteMcp from "./remote-mcp/manifest"
import t3code from "./t3code/manifest"
import telegram from "./telegram/manifest"
import tgbot from "./tgbot/manifest"
import voice from "./voice/manifest"
import waInbox from "./wa-inbox/manifest"
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
  gateway,
  recall,
  mcp,
  http,
  gcal,
  ics,
  "wa-inbox": waInbox,
  t3code,
  printer,
  reminders,
  watches,
  outbound,
  flows,
  jobs,
  proactive,
  chats: chatHistory,
  notes,
  settings,
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
