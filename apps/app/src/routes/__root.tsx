import { HeadContent, Outlet, Scripts, createRootRoute } from "@tanstack/react-router"
import type { ReactNode } from "react"
import { ThemeProvider } from "@/components/theme-provider"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { APP_BASE } from "@/lib/app-path"
import appCss from "@/styles/app.css?url"

// Set the theme class before first paint so there's no light/dark flash.
const THEME_SCRIPT = `(function(){try{
var t=localStorage.getItem('palui.theme')||'system';
var d=t==='dark'||(t!=='light'&&matchMedia('(prefers-color-scheme: dark)').matches);
document.documentElement.classList.add(d?'dark':'light');
document.documentElement.style.colorScheme=d?'dark':'light';
}catch(e){}})();`

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1, viewport-fit=cover" },
      { title: "Palui" },
      { name: "description", content: "Palui Agent dashboard." },
      // Every dashboard route is private (PLAN 10).
      { name: "robots", content: "noindex, nofollow" },
      { name: "theme-color", content: "#fafafa", media: "(prefers-color-scheme: light)" },
      { name: "theme-color", content: "#141417", media: "(prefers-color-scheme: dark)" },
    ],
    links: [
      { rel: "stylesheet", href: appCss },
      { rel: "icon", type: "image/svg+xml", href: `${APP_BASE}/favicon.svg` },
    ],
  }),
  shellComponent: RootDocument,
  component: () => <Outlet />,
})

function RootDocument({ children }: { children: ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_SCRIPT }} />
        <HeadContent />
      </head>
      <body>
        <ThemeProvider>
          <TooltipProvider delay={500}>
            {children}
            <Toaster position="bottom-right" />
          </TooltipProvider>
        </ThemeProvider>
        <Scripts />
      </body>
    </html>
  )
}
