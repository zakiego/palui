// Jobs page. Reads the table directly; writes go through the bridge.
import { getSetting } from "@palui/core/settings"
import { createServerFn } from "@tanstack/react-start"
import { DateTime } from "luxon"
import { z } from "zod"
import { createJob, deleteJob, presets, setJobEnabled, updateJob } from "@/server/jobs-bridge"
import { chatOptions, jobList } from "@/server/queries"
import { callRuntime } from "@/server/runtime"
import { requireUser, requireWriter } from "@/server/session"

export const getJobs = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  const tz = getSetting(db, "timezone")
  return { jobs: jobList(db, tz), chats: chatOptions(db), timezone: tz }
})

export const runJobNow = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    await requireWriter()
    const res = await callRuntime((rt) => rt.runJob(data.id))
    if (!res.ok) throw new Error(res.detail ?? "The runtime didn't start the job.")
    return res
  })

export const setJobEnabledFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100), enabled: z.boolean() }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!setJobEnabled(db, data.id, data.enabled)) throw new Error("That job doesn't exist anymore.")
    return { ok: true }
  })

const ianaZone = z
  .string()
  .min(1)
  .max(64)
  .refine((tz) => DateTime.local().setZone(tz).isValid, "Use an IANA zone like Asia/Makassar.")

const jobInput = z.object({
  id: z.string().max(100).optional(),
  name: z.string().trim().min(1).max(80),
  kind: z.enum(["job", "heartbeat", "briefing"]).optional(),
  rrule: z
    .string()
    .trim()
    .min(5)
    .max(300)
    .regex(/^FREQ=/, "Start the schedule with FREQ=, e.g. FREQ=DAILY;BYHOUR=7;BYMINUTE=0"),
  tz: ianaZone,
  localStart: z.string().regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/),
  prompt: z.string().trim().min(1).max(4000),
  chatId: z.string().min(1).max(100),
  modelRole: z.enum(["default", "fast", "vision"]),
  dailyTokenCap: z.number().int().min(1000).max(5_000_000),
})

export const saveJob = createServerFn({ method: "POST" })
  .validator(jobInput)
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    const { id, ...input } = data
    if (id) {
      const row = updateJob(db, id, input)
      if (!row) throw new Error("That job doesn't exist anymore.")
      return { id: row.id }
    }
    return { id: createJob(db, input).id }
  })

export const deleteJobFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!deleteJob(db, data.id)) throw new Error("That job doesn't exist anymore.")
    return { ok: true }
  })

export const getJobPreset = createServerFn({ method: "GET" })
  .validator(z.object({ preset: z.enum(["dailyBriefing", "heartbeat"]), chatId: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    return presets[data.preset](data.chatId, getSetting(db, "timezone"))
  })
