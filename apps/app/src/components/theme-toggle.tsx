/**
 * The one-press switch between light and dark, in the header and on the login
 * pages. The theme starts on the system setting; the first press pins the
 * opposite of whatever is showing. Going back to System is in the account menu.
 *
 * The accessible name says where the button goes, not where you are, because
 * that is what pressing it does.
 */

import { MoonIcon, SunIcon } from "lucide-react"

import { IconSwap } from "@/components/icon-swap"
import { useTheme } from "@/components/theme-provider"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

export function ThemeToggle({ className }: { className?: string }) {
  const { resolvedTheme, setTheme } = useTheme()
  const dark = resolvedTheme === "dark"
  const label = dark ? "Switch to light theme" : "Switch to dark theme"

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon"
            className={className}
            onClick={() => setTheme(dark ? "light" : "dark")}
          />
        }
      >
        <IconSwap active={dark} from={<SunIcon />} to={<MoonIcon />} />
      </TooltipTrigger>
      <TooltipContent>
        {label}
      </TooltipContent>
    </Tooltip>
  )
}
