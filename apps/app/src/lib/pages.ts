// The seven dashboard pages (PLAN 11.4), for the sidebar, header and ⌘K.
import { ActivityIcon, BellIcon, CalendarClockIcon, HouseIcon, MessagesSquareIcon, Settings2Icon, ToggleRightIcon, type LucideIcon } from "lucide-react"
import type { Key } from "@/lib/i18n"

export type Page = { to: "/" | "/chats" | "/reminders" | "/jobs" | "/connectors" | "/activity" | "/settings"; label: Key; Icon: LucideIcon; group: Key }

export const PAGES: Page[] = [
  { to: "/", label: "nav.home", Icon: HouseIcon, group: "nav.group.agent" },
  { to: "/chats", label: "nav.chats", Icon: MessagesSquareIcon, group: "nav.group.agent" },
  { to: "/reminders", label: "nav.reminders", Icon: BellIcon, group: "nav.group.agent" },
  { to: "/jobs", label: "nav.jobs", Icon: CalendarClockIcon, group: "nav.group.agent" },
  { to: "/connectors", label: "nav.connectors", Icon: ToggleRightIcon, group: "nav.group.system" },
  { to: "/activity", label: "nav.activity", Icon: ActivityIcon, group: "nav.group.system" },
  { to: "/settings", label: "nav.settings", Icon: Settings2Icon, group: "nav.group.system" },
]

// The router reports paths without the /app basepath.
export function pageFor(pathname: string): Page | undefined {
  const p = pathname.replace(/^\/app(?=\/|$)/, "") || "/"
  return PAGES.find((page) => (page.to === "/" ? p === "/" : p === page.to || p.startsWith(`${page.to}/`)))
}
