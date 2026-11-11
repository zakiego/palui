// Which paths on palui.zakiego.com belong to the app on the Docker host
// (through the tunnel) and which to the static site on Cloudflare Pages.
//
// The app owns:
//   /app/*       the dashboard, its assets and its server functions
//   /api/*       the REST API: Better Auth (/api/auth), inbound hooks
//                (/api/hooks/:slug) and the dashboard's own endpoints
//   /health      the healthcheck
//
// Everything else ("/", /docs, /_next, /og, /search.json, robots.txt...) is
// the site. The site's search index lives at /search.json, not /api/search,
// because /api belongs to the app here.
const APP_PREFIXES = ["/app", "/api", "/health"]

const underAny = (pathname: string, prefixes: string[]) =>
  prefixes.some((p) => pathname === p || pathname.startsWith(`${p}/`))

export function isAppPath(pathname: string): boolean {
  return underAny(pathname, APP_PREFIXES)
}
