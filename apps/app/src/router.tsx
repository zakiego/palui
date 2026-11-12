import { createRouter } from "@tanstack/react-router"
import { routeTree } from "./routeTree.gen"

// How long a page's loaded data stays usable after you leave it. Coming back
// inside this window shows it at once and refreshes it behind the page. The
// shell loads every sidebar page again well before this runs out (use-warm-pages).
const KEEP_MS = 30 * 60_000

export function getRouter() {
  return createRouter({
    routeTree,
    scrollRestoration: true,
    // Start loading the moment a link is hovered or touched, not 50 ms later.
    defaultPreload: "intent",
    defaultPreloadDelay: 0,
    defaultGcTime: KEEP_MS,
    defaultPreloadGcTime: KEEP_MS,
    // Show the page's own Skeleton quickly instead of a blank wait.
    defaultPendingMs: 300,
    defaultPendingMinMs: 300,
  })
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
