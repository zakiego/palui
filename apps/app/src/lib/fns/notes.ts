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
import { getAgent, getDefaultAgent, listAgents } from "@palui/core/agents"
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { requireUser, requireWriter } from "@/server/session"

// NotesError messages are bot-facing Indonesian; the dashboard gets its own.
function rethrow(err: unknown): never {
  if (err instanceof NotesError) throw new Error(err.message)
  throw err
}

// Instructions are per agent (PLAN 19.1); preferences are the owner's.
export const getNotes = createServerFn({ method: "GET" })
  .validator(z.object({ agentId: z.string().min(1).max(100).optional() }))
  .handler(async ({ data }) => {
    const { db } = await requireUser()
    const agent = (data.agentId ? getAgent(db, data.agentId) : undefined) ?? getDefaultAgent(db)
    return {
      agent: { id: agent.id, name: agent.name, isDefault: agent.isDefault },
      agents: listAgents(db).map((a) => ({ id: a.id, name: a.name })),
      instructions: listInstructions(db, { agentId: agent.id }),
      preferences: listPreferences(db),
    }
  })

export const saveInstructionFn = createServerFn({ method: "POST" })
  .validator(
    z.object({
      id: z.string().min(1).max(100).optional(),
      agentId: z.string().min(1).max(100).optional(),
      title: z.string().trim().min(1).max(MAX_INSTRUCTION_TITLE),
      body: z.string().trim().min(1).max(MAX_INSTRUCTION_BODY),
    }),
  )
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    try {
      const row = data.id ? updateInstruction(db, data.id, { title: data.title, body: data.body }) : createInstruction(db, { title: data.title, body: data.body, agentId: data.agentId })
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
