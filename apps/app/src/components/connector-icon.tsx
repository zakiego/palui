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
  MicIcon,
  MonitorIcon,
  NetworkIcon,
  PlugIcon,
  SendIcon,
  SquareTerminalIcon,
  WebhookIcon,
  type LucideIcon,
} from "lucide-react"

const ICONS: Record<string, LucideIcon> = {
  send: SendIcon,
  "message-circle": MessageCircleIcon,
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
}

export function connectorIcon(name: string | undefined): LucideIcon {
  return (name && ICONS[name]) || PlugIcon
}
