// Flows pages (RFC flows 10): read-only. Flows are built and changed from chat.
import { getSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { flowDetail, flowList, routinesStatus } from "@/server/flows"
import { requireUser } from "@/server/session"

export const getFlows = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  return { flows: flowList(db), routines: routinesStatus(db), timezone: getSetting(db, "timezone") }
})

export const getFlowFn = createServerFn({ method: "GET" })
  .validator(z.object({ id: z.string().min(1).max(64) }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    return { flow: flowDetail(db, data.id), timezone: getSetting(db, "timezone") }
  })
