// Routines page. Reads and writes go through server/routines.ts, which wraps the
// same service the agent's routine tools use. Editing a routine stays in chat.
import { getSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { pauseRoutine, removeRoutine, resumeRoutine, routineDetail, routineList } from "@/server/routines"
import { requireUser, requireWriter } from "@/server/session"

const routineId = z.string().min(1).max(100)
const isoDate = z.string().regex(/^\d{4}-\d{2}-\d{2}$/)

export const getRoutines = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  return { routines: routineList(db), timezone: getSetting(db, "timezone") }
})

export const getRoutine = createServerFn({ method: "GET" })
  .validator(z.object({ id: routineId, from: isoDate.optional(), to: isoDate.optional() }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    return { detail: routineDetail(db, data.id, { from: data.from, to: data.to }), timezone: getSetting(db, "timezone") }
  })

export const pauseRoutineFn = createServerFn({ method: "POST" })
  .validator(
    z.object({
      id: routineId,
      // Wall-clock time in the routine's zone; omit to pause until resumed.
      until: z
        .string()
        .regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
        .nullable()
        .default(null),
    }),
  )
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const r = pauseRoutine(db, data.id, data.until)
    return { ok: true, pausedUntil: r.pausedUntil }
  })

export const resumeRoutineFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: routineId }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    resumeRoutine(db, data.id)
    return { ok: true }
  })

export const deleteRoutineFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: routineId }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    removeRoutine(db, data.id)
    return { ok: true }
  })
