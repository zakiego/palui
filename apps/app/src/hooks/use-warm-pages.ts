// Loads every sidebar page's code and data ahead of the click, so a page
// switch has nothing left to wait for. The router keeps what it loaded and
// shows it at once, then refreshes it behind the page.
import * as React from "react"
import { useRouter } from "@tanstack/react-router"

import { PAGES } from "@/lib/pages"

/** How often an open tab loads the pages again, well inside the router's cache lifetime (see router.tsx). */
const REWARM_MS = 5 * 60_000
/** Coming back to the tab loads them again, but not more often than this. */
const MIN_GAP_MS = 30_000

export function useWarmPages() {
  const router = useRouter()

  React.useEffect(() => {
    // Someone on a metered connection asked the browser to hold back; loading on hover still works.
    if ((navigator as { connection?: { saveData?: boolean } }).connection?.saveData) return

    let last = 0
    const warm = () => {
      if (document.visibilityState !== "visible" || Date.now() - last < MIN_GAP_MS) return
      last = Date.now()
      for (const { to } of PAGES) router.preloadRoute({ to }).catch(() => {})
    }

    // After the first paint, not in its way.
    const first = window.setTimeout(warm, 300)
    const again = window.setInterval(warm, REWARM_MS)
    document.addEventListener("visibilitychange", warm)
    return () => {
      window.clearTimeout(first)
      window.clearInterval(again)
      document.removeEventListener("visibilitychange", warm)
    }
  }, [router])
}
