// The hooks skill has no tools: the app verifies and ingests requests itself
// (@palui/core/hooks). The connector exists so hooks can be paused like any
// other connector (verifyHookRequest answers 503 while it's paused).
import { listHooks } from "@palui/core/hooks/index"
import { defineRuntime } from "@palui/core/connectors/define"
import manifest from "./manifest"

export default defineRuntime(manifest, {
  async test(ctx) {
    const n = listHooks(ctx.db).length
    return { ok: true, detail: n ? `${n} hook${n === 1 ? "" : "s"} set up` : "Ready. Create a hook to get its URL and secret." }
  },
  async start(ctx) {
    return {
      async stop() {},
      async tools() {
        return {}
      },
      live() {
        const rows = listHooks(ctx.db)
        return {
          hooks: rows.length,
          enabled: rows.filter((h) => h.enabled).length,
          lastAt: rows.reduce<number | null>((max, h) => (h.lastAt && (!max || h.lastAt > max) ? h.lastAt : max), null),
        }
      },
    }
  },
})
