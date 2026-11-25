// The sidebar footer from the sidebar-07 block: who is signed in, the theme,
// Settings and Log out.
import { Link } from "@tanstack/react-router"
import { ChevronsUpDownIcon, LogOutIcon, MonitorIcon, MoonIcon, Settings2Icon, SunIcon, type LucideIcon } from "lucide-react"

import { useTheme, type Theme } from "@/components/theme-provider"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem, useSidebar } from "@/components/ui/sidebar"
import { useT, type Key } from "@/lib/i18n"

const THEMES = [
  { value: "system", label: "theme.system", Icon: MonitorIcon },
  { value: "light", label: "theme.light", Icon: SunIcon },
  { value: "dark", label: "theme.dark", Icon: MoonIcon },
] as const satisfies readonly { value: Theme; label: Key; Icon: LucideIcon }[]

export function NavUser({ username, onLogout }: { username: string; onLogout: () => void }) {
  const { isMobile, setOpenMobile } = useSidebar()
  const t = useT()
  const { theme, setTheme } = useTheme()
  // The first character as a reader sees it: slice(0, 1) would halve an emoji.
  const initial = (Array.from(username.trim())[0] ?? "O").toUpperCase()
  const who = (
    <>
      <Avatar className="size-6 rounded-full">
        <AvatarFallback className="rounded-full text-xs">{initial}</AvatarFallback>
      </Avatar>
      <span className="min-w-0 flex-1 truncate text-start text-caption font-medium text-foreground">{username}</span>
    </>
  )
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger render={<SidebarMenuButton className="aria-expanded:bg-sidebar-accent" aria-label={`${username}, account menu`} />}>
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
            <DropdownMenuRadioGroup<Theme> value={theme} onValueChange={setTheme}>
              <DropdownMenuLabel>{t("theme.label")}</DropdownMenuLabel>
              {THEMES.map(({ value, label, Icon }) => (
                <DropdownMenuRadioItem key={value} value={value}>
                  <Icon aria-hidden="true" />
                  {t(label)}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
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
