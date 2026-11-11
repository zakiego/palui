// The dashboard lives under /app (the router basepath). The REST API (/api),
// Better Auth (/api/auth) and /health stay at the root. In production the edge
// Worker sends every other root path to the static site.
export const APP_BASE = "/app"
