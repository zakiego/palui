// Notes page: the owner's standing instructions and preferences. Both are read
// by the runtime at the start of each turn, so writes go straight to the table.
import {
  createInstruction,
  deleteInstruction,
  deletePreference,
  listInstructions,
  listPreferences,
  MAX_INSTRUCTION_BODY,
  MAX_INSTRUCTION_TITLE,
  MAX_PREFERENCE_VALUE,
  NotesError,
  setPreference,
  updateInstruction,
} from "@palui/core/notes/service"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { requireUser, requireWriter } from "@/server/session"

// NotesError messages are bot-facing Indonesian; the dashboard gets its own.
function rethrow(err: unknown): never {
  if (err instanceof NotesError) throw new Error(err.message)
  throw err
}

export const getNotes = createServerFn({ method: "GET" }).handler(async () => {
  const { db } = await requireUser()
  return { instructions: listInstructions(db), preferences: listPreferences(db) }
})

export const saveInstructionFn = createServerFn({ method: "POST" })
  .validator(
    z.object({
      id: z.string().min(1).max(100).optional(),
      title: z.string().trim().min(1).max(MAX_INSTRUCTION_TITLE),
      body: z.string().trim().min(1).max(MAX_INSTRUCTION_BODY),
    }),
  )
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    try {
      const row = data.id ? updateInstruction(db, data.id, { title: data.title, body: data.body }) : createInstruction(db, { title: data.title, body: data.body })
      return { id: row.id }
    } catch (err) {
      rethrow(err)
    }
  })

export const setInstructionEnabledFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100), enabled: z.boolean() }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    try {
      updateInstruction(db, data.id, { enabled: data.enabled })
      return { ok: true }
    } catch (err) {
      rethrow(err)
    }
  })

export const deleteInstructionFn = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(100) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!deleteInstruction(db, data.id)) throw new Error("That instruction doesn't exist anymore.")
    return { ok: true }
  })

export const setPreferenceFn = createServerFn({ method: "POST" })
  .validator(z.object({ key: z.string().trim().min(1).max(40), value: z.string().trim().min(1).max(MAX_PREFERENCE_VALUE) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    try {
      const row = setPreference(db, data.key, data.value)
      return { key: row.key }
    } catch (err) {
      rethrow(err)
    }
  })

export const deletePreferenceFn = createServerFn({ method: "POST" })
  .validator(z.object({ key: z.string().min(1).max(40) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    deletePreference(db, data.key)
    return { ok: true }
  })
