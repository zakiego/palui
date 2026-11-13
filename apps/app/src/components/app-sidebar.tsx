// The shell sidebar, from the sidebar-07 layout: collapses to icons on a
// desktop (rail or ⌘B), becomes a Sheet on a phone.
import * as React from "react"
import { Link, useRouterState } from "@tanstack/react-router"

import { NavUser } from "@/components/nav-user"
import { PaluiMark } from "@/components/palui-mark"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar"
import { useT, type Key } from "@/lib/i18n"
import { PAGES, pageFor } from "@/lib/pages"

const GROUPS: Key[] = ["nav.group.agent", "nav.group.system"]

export function AppSidebar({ username, onLogout, ...props }: { username: string; onLogout: () => void } & React.ComponentProps<typeof Sidebar>) {
  const { setOpenMobile } = useSidebar()
  const t = useT()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const current = pageFor(pathname)
  const close = () => setOpenMobile(false)

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" render={<Link to="/" onClick={close} />} tooltip={t("app.name")}>
              <PaluiMark className="size-8" />
              <span className="truncate text-sm font-semibold text-foreground">{t("app.name")}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <nav aria-label="Pages" className="contents">
          {GROUPS.map((group) => (
            <SidebarGroup key={group} role="group" aria-labelledby={`nav-${group}`}>
              <SidebarGroupLabel id={`nav-${group}`}>{t(group)}</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu>
                  {PAGES.filter((p) => p.group === group).map(({ to, label, Icon }) => {
                    const active = current?.to === to
                    return (
                      <SidebarMenuItem key={to}>
                        <SidebarMenuButton isActive={active} tooltip={t(label)} render={<Link to={to} aria-current={active ? "page" : undefined} onClick={close} />}>
                          <Icon aria-hidden="true" />
                          <span>{t(label)}</span>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )
                  })}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          ))}
        </nav>
      </SidebarContent>

      <SidebarFooter>
        <NavUser username={username} onLogout={onLogout} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
