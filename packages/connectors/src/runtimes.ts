// Every connector runtime, keyed by manifest id. Imported by the runtime only.
import type { AnyRuntime } from "@palui/core/connectors/define"
import attachments from "./attachments/runtime"
import chatHistory from "./chats/runtime"
import aiGateway from "./ai-gateway/runtime"
import cliproxy from "./cliproxy/runtime"
import gcal from "./gcal/runtime"
import hooks from "./hooks/runtime"
import jobs from "./jobs/runtime"
import mcp from "./mcp/runtime"
import notes from "./notes/runtime"
import recall from "./recall/runtime"
import reminders from "./reminders/runtime"
import remoteMcp from "./remote-mcp/runtime"
import t3code from "./t3code/runtime"
import telegram from "./telegram/runtime"
import voice from "./voice/runtime"
import web from "./web/runtime"
import webfetch from "./webfetch/runtime"
import whatsapp from "./whatsapp/runtime"

export const runtimes: Record<string, AnyRuntime> = {
  telegram,
  whatsapp,
  web,
  cliproxy,
  "ai-gateway": aiGateway,
  recall,
  mcp,
  gcal,
  t3code,
  reminders,
  jobs,
  chats: chatHistory,
  notes,
  "remote-mcp": remoteMcp,
  attachments,
  voice,
  webfetch,
  hooks,
}
