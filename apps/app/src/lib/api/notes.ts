// Notes page: the owner's standing instructions (per agent) and preferences.
import { withInput } from "./client"
import type { Instruction, Ok } from "./types"

export type Preference = { key: string; updatedAt: number; value: string }

export type NotesPage = {
  agent: { id: string; name: string; isDefault: boolean }
  agents: { id: string; name: string }[]
  instructions: Instruction[]
  preferences: Preference[]
}

export const getNotes = withInput<{ agentId?: string }, NotesPage>("getNotes", "GET")
export const saveInstructionFn = withInput<{ id?: string; agentId?: string; title: string; body: string }, { id: string }>("saveInstructionFn", "POST")
export const setInstructionEnabledFn = withInput<{ id: string; enabled: boolean }, Ok>("setInstructionEnabledFn", "POST")
export const deleteInstructionFn = withInput<{ id: string }, Ok>("deleteInstructionFn", "POST")
export const setPreferenceFn = withInput<{ key: string; value: string }, { key: string }>("setPreferenceFn", "POST")
export const deletePreferenceFn = withInput<{ key: string }, Ok>("deletePreferenceFn", "POST")
