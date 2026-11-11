// The dashboard pages (PLAN 11.4), for the sidebar, header and ⌘K.
import { ActivityIcon, BotIcon, PaperclipIcon, BellIcon, CalendarClockIcon, HouseIcon, MessagesSquareIcon, NotebookPenIcon, RepeatIcon, Settings2Icon, ToggleRightIcon, WorkflowIcon, type LucideIcon } from "lucide-react"
import type { Key } from "@/lib/i18n"

export type Page = { to: "/" | "/chats" | "/agents" | "/reminders" | "/jobs" | "/routines" | "/flows" | "/notes" | "/attachments" | "/connectors" | "/activity" | "/settings"; label: Key; Icon: LucideIcon; group: Key }

export const PAGES: Page[] = [
  { to: "/", label: "nav.home", Icon: HouseIcon, group: "nav.group.agent" },
  { to: "/chats", label: "nav.chats", Icon: MessagesSquareIcon, group: "nav.group.agent" },
  { to: "/agents", label: "nav.agents", Icon: BotIcon, group: "nav.group.agent" },
  { to: "/reminders", label: "nav.reminders", Icon: BellIcon, group: "nav.group.agent" },
  { to: "/jobs", label: "nav.jobs", Icon: CalendarClockIcon, group: "nav.group.agent" },
  { to: "/routines", label: "nav.routines", Icon: RepeatIcon, group: "nav.group.agent" },
  { to: "/flows", label: "nav.flows", Icon: WorkflowIcon, group: "nav.group.agent" },
  { to: "/notes", label: "nav.notes", Icon: NotebookPenIcon, group: "nav.group.agent" },
  { to: "/attachments", label: "nav.attachments", Icon: PaperclipIcon, group: "nav.group.agent" },
  { to: "/connectors", label: "nav.connectors", Icon: ToggleRightIcon, group: "nav.group.system" },
  { to: "/activity", label: "nav.activity", Icon: ActivityIcon, group: "nav.group.system" },
  { to: "/settings", label: "nav.settings", Icon: Settings2Icon, group: "nav.group.system" },
]

// The router reports paths without the /app basepath.
export function pageFor(pathname: string): Page | undefined {
  const p = pathname.replace(/^\/app(?=\/|$)/, "") || "/"
  return PAGES.find((page) => (page.to === "/" ? p === "/" : p === page.to || p.startsWith(`${page.to}/`)))
}
