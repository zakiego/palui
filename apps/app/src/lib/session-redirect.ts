// A 401 from the server (or a stream's `unauthorized` event) means the session
// is gone: forget the remembered viewer and go to the login page, which brings
// the owner back here after signing in. Pages that work signed out stay put.
import { onUnauthorized } from "@/lib/api/client"
import { forgetViewer } from "@/lib/viewer"

const SIGNED_OUT_ROUTES = new Set(["/login", "/invite/$token"])

type RouterLike = {
  state: { matches: { routeId: string }[]; location: { href: string } }
  navigate: (opts: { to: "/login"; search: { next: string } }) => Promise<unknown>
}

// The router moves `location` as soon as a navigation starts but `matches`
// only once it has loaded, so a second 401 (the page's RPCs and the event
// stream usually fail together) must also look at the path it is going to.
function onSignedOutPage(href: string): boolean {
  const path = href.split(/[?#]/)[0] ?? ""
  return path === "/login" || path.startsWith("/invite/")
}

export function redirectToLoginOnUnauthorized(router: RouterLike): () => void {
  let pending = false
  return onUnauthorized(() => {
    forgetViewer()
    if (pending) return
    if (router.state.matches.some((m) => SIGNED_OUT_ROUTES.has(m.routeId))) return
    if (onSignedOutPage(router.state.location.href)) return
    pending = true
    void router.navigate({ to: "/login", search: { next: router.state.location.href } }).finally(() => {
      pending = false
    })
  })
}
