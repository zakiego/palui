// Read-only job listing for the model (PLAN 7). Creating and editing jobs stays in
// the dashboard.
import { tool } from "ai"
import { z } from "zod"
import type { DB } from "@palui/db"
import { describeJobSchedule, describeNextRun, listJobs } from "@palui/core/jobs/service"
import { defineRuntime, type PaluiTool } from "@palui/core/saklar/define"
import manifest from "./manifest"

export function jobTools(db: DB, clock: () => number = () => Date.now()): Record<string, PaluiTool> {
  return {
    daftar_jadwal: {
      class: "read",
      title: "List scheduled jobs",
      tool: tool({
        description: "List scheduled jobs (morning briefing, heartbeat, custom prompts) with their schedule, next run and last result. Jobs are created and edited in the dashboard.",
        inputSchema: z.object({
          includePaused: z.boolean().default(true).describe("Include paused jobs."),
        }),
        execute: async ({ includePaused }) => {
          const now = clock()
          const rows = listJobs(db, includePaused ? {} : { enabled: true })
          return {
            ok: true as const,
            count: rows.length,
            jobs: rows.map((j) => ({
              id: j.id,
              name: j.name,
              kind: j.kind,
              enabled: j.enabled,
              schedule: describeJobSchedule(j, now),
              next: describeNextRun(j, now) ?? (j.enabled ? "Selesai (aturan ulangnya udah habis)" : "Dijeda"),
              lastStatus: j.lastStatus,
              lastResult: j.lastResult ? j.lastResult.slice(0, 200) : null,
            })),
            message: rows.length ? undefined : "Belum ada jadwal. Bikin di dashboard, halaman Jobs.",
          }
        },
      }),
    },
  }
}

export default defineRuntime(manifest, {
  test: async (ctx) => {
    const active = listJobs(ctx.db, { enabled: true }).length
    return { ok: true, detail: active ? `${active} active job${active === 1 ? "" : "s"}` : "Ready, no active jobs" }
  },
  start: async (ctx) => ({
    stop: async () => {},
    tools: async () => jobTools(ctx.db),
  }),
})
