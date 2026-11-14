import * as React from "react"
import { Outlet, createRootRoute, useRouterState } from "@tanstack/react-router"
import { ThemeProvider } from "@/components/theme-provider"
import { Toaster } from "@/components/ui/sonner"

// index.html carries the document head (meta tags, favicon, the theme script
// that prevents a light/dark flash, the stylesheet). Routes only set a title.
export const Route = createRootRoute({
  head: () => ({ meta: [{ title: "Palui" }] }),
  component: RootComponent,
})

function RootComponent() {
  return (
    <ThemeProvider>
      <RouteTitle />
      <Outlet />
      <Toaster position="bottom-right" />
    </ThemeProvider>
  )
}

const DEFAULT_TITLE = "Palui"

// The deepest matched route's `head` title becomes document.title.
export function RouteTitle() {
  const title = useRouterState({
    select: (s) => {
      for (let i = s.matches.length - 1; i >= 0; i--) {
        for (const m of s.matches[i]?.meta ?? []) {
          if (m && typeof m.title === "string" && m.title) return m.title
        }
      }
      return DEFAULT_TITLE
    },
  })
  React.useEffect(() => {
    document.title = title
  }, [title])
  return null
}
