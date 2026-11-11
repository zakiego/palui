// Activity page: events, runs, usage.
import { getSetting } from "@palui/core/settings"
import { listConnectors } from "@palui/core/connectors/store"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { eventCount, eventList, recentRuns, runCount, usage } from "@/server/queries"
import { requireUser } from "@/server/session"

const level = z.enum(["debug", "info", "warn", "error"])

export const getEvents = createServerFn({ method: "GET" })
  .validator(z.object({ connectorId: z.string().max(64).optional(), levels: z.array(level).max(4).optional() }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    return {
      events: eventList(db, { connectorId: data.connectorId, levels: data.levels, limit: 300 }),
      total: eventCount(db, { connectorId: data.connectorId, levels: data.levels }),
      connectors: listConnectors(db).map((c) => ({ id: c.id, label: c.label })),
      timezone: getSetting(db, "timezone"),
    }
  })

export const getRuns = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  return { runs: recentRuns(db, 100), total: runCount(db), timezone: getSetting(db, "timezone") }
})

export const getUsage = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const tz = getSetting(db, "timezone")
  return { ...usage(db, tz, 14), timezone: tz }
})
