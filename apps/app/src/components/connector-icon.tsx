// Manifests name a lucide icon as a string (they can't import React). This maps
// the names in use to components, with a plug as the fallback.
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
  type LucideIcon,
} from "lucide-react"

const ICONS: Record<string, LucideIcon> = {
  send: SendIcon,
  "message-circle": MessageCircleIcon,
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

export function connectorIcon(name: string | undefined): LucideIcon {
  return (name && ICONS[name]) || PlugIcon
}
