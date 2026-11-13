// The sidebar footer from the sidebar-07 block: who is signed in, with
// Settings and Log out.
import { Link } from "@tanstack/react-router"
import { ChevronsUpDownIcon, LogOutIcon, Settings2Icon } from "lucide-react"

import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem, useSidebar } from "@/components/ui/sidebar"
import { useT } from "@/lib/i18n"

export function NavUser({ username, onLogout }: { username: string; onLogout: () => void }) {
  const { isMobile, setOpenMobile } = useSidebar()
  const t = useT()
  // The first character as a reader sees it: slice(0, 1) would halve an emoji.
  const initial = (Array.from(username.trim())[0] ?? "O").toUpperCase()
  const who = (
    <>
      <Avatar className="rounded-lg">
        <AvatarFallback className="rounded-lg">{initial}</AvatarFallback>
      </Avatar>
      <span className="grid flex-1 text-start text-sm leading-tight">
        <span className="truncate font-medium">{username}</span>
        <span className="truncate text-xs text-muted-foreground">{t("app.tagline")}</span>
      </span>
    </>
  )
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger render={<SidebarMenuButton size="lg" className="aria-expanded:bg-muted" aria-label={`${username}, account menu`} />}>
            {who}
            <ChevronsUpDownIcon className="ms-auto" aria-hidden="true" />
          </DropdownMenuTrigger>
          <DropdownMenuContent className="min-w-56" side={isMobile ? "bottom" : "right"} align="end" sideOffset={4}>
            <DropdownMenuGroup>
              <DropdownMenuLabel className="p-0 font-normal">
                <span className="flex items-center gap-2 px-1 py-1.5 text-start text-sm">{who}</span>
              </DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              <DropdownMenuItem render={<Link to="/settings" onClick={() => setOpenMobile(false)} />}>
                <Settings2Icon aria-hidden="true" />
                {t("nav.settings")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={onLogout}>
                <LogOutIcon aria-hidden="true" />
                {t("nav.logout")}
              </DropdownMenuItem>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
