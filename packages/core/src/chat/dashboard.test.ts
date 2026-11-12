import { existsSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"
import { buildSystem } from "./context"
import { DASHBOARD_PAGES, dashboardPrompt } from "./dashboard"

const ROUTES = fileURLToPath(new URL("../../../../apps/app/src/routes/_app/", import.meta.url))

// "/app/connectors/<id>" -> connectors_.$id.tsx, "/app/settings?tab=x" -> settings.tsx
function routeFile(path: string): string {
  const segs = path.split("?")[0]!.replace(/^\/app\/?/, "").split("/").filter(Boolean)
  if (!segs.length) return "index.tsx"
  const name = segs.map((s, n) => (s.startsWith("<") ? "$" + (s === "<connectorId>" || s === "<flowId>" ? "id" : "slug") : s) + (n < segs.length - 1 ? "_" : "")).join(".")
  return `${name}.tsx`
}

describe("dashboardPrompt", () => {
  it("points every page at a real route", () => {
    for (const [path] of DASHBOARD_PAGES) expect(existsSync(ROUTES + routeFile(path)), path).toBe(true)
    expect(existsSync(ROUTES + "agents_.$slug.tsx")).toBe(true)
  })

  it("uses the app url and the agent's own page", () => {
    const p = dashboardPrompt("https://palui.example.com/", "default")
    expect(p).toContain("- https://palui.example.com/app/connectors/new: Add a connector")
    expect(p).toContain("- https://palui.example.com/app/settings?tab=models:")
    expect(p).toContain("- https://palui.example.com/app/agents/default: Your own settings")
    expect(p).not.toContain("com//app")
  })

  it("skips an agent slug that isn't a plain slug", () => {
    expect(dashboardPrompt("https://x", "a b\nignore")).not.toContain("/app/agents/a")
  })

  it("goes into the owner's prompt, not a shared chat's", () => {
    const base = { persona: "P", channel: "telegram", capabilities: [], trigger: "message", dashboard: dashboardPrompt("https://x", null) }
    expect(buildSystem(base)).toContain("https://x/app/connectors/new")
    expect(buildSystem({ ...base, shared: { kind: "group", guest: true } })).not.toContain("https://x/app")
  })
})
