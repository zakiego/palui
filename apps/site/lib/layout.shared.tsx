import type { BaseLayoutProps, LinkItemType } from "fumadocs-ui/layouts/shared"
import { LayoutDashboardIcon } from "lucide-react"
import { PaluiMark } from "@/components/palui-mark"
import { appName } from "./shared"

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <span className="flex items-center gap-2 font-semibold tracking-tight">
          <PaluiMark />
          {appName}
        </span>
      ),
    },
    links: [{ text: "Docs", url: "/docs", active: "nested-url" }, dashboardLink],
  }
}

// The dashboard is the app on the same host, not a page of this site, so it's
// a plain external link (no client-side routing into /app). It only works for
// the owner.
export const dashboardLink: LinkItemType = {
  type: "icon",
  icon: <LayoutDashboardIcon />,
  text: "Dashboard",
  label: "Owner sign-in to the dashboard",
  url: "/app",
  external: true,
}
