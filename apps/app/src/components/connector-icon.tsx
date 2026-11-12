// Manifests name a lucide icon as a string (they can't import React). This maps
// the names in use to components, with a plug as the fallback. Third-party
// services with a real logo (WhatsApp, Telegram) map to it instead of a lucide glyph.
import type * as React from "react"
import {
  BellIcon,
  BrainIcon,
  CalendarClockIcon,
  CalendarIcon,
  CpuIcon,
  GlobeIcon,
  MessageCircleIcon,
  MessagesSquareIcon,
  MicIcon,
  MonitorIcon,
  NetworkIcon,
  NotebookPenIcon,
  PaperclipIcon,
  PlugIcon,
  RepeatIcon,
  SendIcon,
  SettingsIcon,
  ServerCogIcon,
  SquareTerminalIcon,
  WebhookIcon,
  WorkflowIcon,
} from "lucide-react"

import { TelegramIcon, WhatsAppIcon } from "@/components/brand-icons"

type Icon = React.ComponentType<React.SVGProps<SVGSVGElement>>

const ICONS: Record<string, Icon> = {
  send: SendIcon,
  "message-circle": MessageCircleIcon,
  whatsapp: WhatsAppIcon,
  telegram: TelegramIcon,
  "messages-square": MessagesSquareIcon,
  monitor: MonitorIcon,
  cpu: CpuIcon,
  network: NetworkIcon,
  brain: BrainIcon,
  plug: PlugIcon,
  bell: BellIcon,
  "calendar-clock": CalendarClockIcon,
  calendar: CalendarIcon,
  mic: MicIcon,
  globe: GlobeIcon,
  webhook: WebhookIcon,
  "square-terminal": SquareTerminalIcon,
  "notebook-pen": NotebookPenIcon,
  "server-cog": ServerCogIcon,
  paperclip: PaperclipIcon,
  repeat: RepeatIcon,
  workflow: WorkflowIcon,
  settings: SettingsIcon,
}

export function connectorIcon(name: string | undefined): Icon {
  return (name && ICONS[name]) || PlugIcon
}
