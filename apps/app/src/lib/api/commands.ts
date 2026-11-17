// Commands page: the owner's custom chat commands (PLAN 8.3), each a saved
// prompt under a /name, for every agent (agentId null) or one agent.
import { noInput, withInput } from "./client"
import type { Ok } from "./types"

export type CustomCommand = {
  id: string
  agentId: string | null
  name: string
  description: string
  prompt: string
  attachReport: boolean
  enabled: boolean
  createdAt: number
  updatedAt: number
}

export type CommandsPage = {
  commands: CustomCommand[]
  agents: { id: string; name: string }[]
  builtins: string[]
}

export type SaveCommand = {
  id?: string
  agentId: string | null
  name: string
  description: string
  prompt: string
  attachReport: boolean
  enabled: boolean
}

export const getCommandsPage = noInput<CommandsPage>("getCommandsPage", "GET")
export const saveCommandFn = withInput<SaveCommand, { id: string }>("saveCommandFn", "POST")
export const setCommandEnabledFn = withInput<{ id: string; enabled: boolean }, Ok>("setCommandEnabledFn", "POST")
export const deleteCommandFn = withInput<{ id: string }, Ok>("deleteCommandFn", "POST")
