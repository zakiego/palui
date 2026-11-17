// Agents page and an agent's page: persona, shared-chat rules, daily cap,
// recall container, Telegram bot, tools and memory, and the per-chat switches.
import { withInput, noInput } from "./client"
import type { AgentColor, AgentIcon } from "@/shared/constants"
import type { ModelRoles } from "./settings"
import type { AgentView, Channel, ChatKind, ConnectorStatus, GuestTools, Instruction, Ok, ReplyMode, TelegramBot, ToolClass, ToolPermission, ToolPermissions } from "./types"

export type AgentsPage = { agents: AgentView[]; bots: TelegramBot[] }

export type AgentChatRow = {
  id: string
  title: string
  channel: Channel
  kind: ChatKind
  lastAt: number
  allowed: boolean
  replyMode: ReplyMode
  ack: boolean
  ackEmoji: string
  deployNotice: boolean
}

// `agent` is null when the slug doesn't exist (and nothing else is sent).
export type AgentPage =
  | { agent: null; models?: undefined; bots?: undefined; instructions?: undefined; chats?: undefined; chatsTotal?: undefined }
  | { agent: AgentView; models: AgentModels; bots: TelegramBot[]; instructions: Instruction[]; chats: AgentChatRow[]; chatsTotal: number }

// The agent's own chains (a role left empty uses the global one), the global
// chains, and the model connectors a step can pick.
export type AgentModels = { own: ModelRoles; global: ModelRoles; connectors: { id: string; label: string; status: ConnectorStatus; icon?: string }[] }

export type CatalogTool = {
  name: string
  tool: string
  connectorId: string
  connectorLabel: string
  connectorType: string
  class: ToolClass
  title: string | null
  untrusted: boolean
  guestSafe: boolean
  // A members turn gets it too. The server leaves it out when false.
  memberSafe?: boolean
  // Whose data it touches: each chat's, this agent's, every agent's, or none.
  // Left out, it follows the server: this agent's when it owns it, else shared.
  data?: DataScope
}

export type DataScope = "chat" | "agent" | "shared" | "none"

// tools is null when the runtime couldn't list them (error says why).
export type AgentTools = { tools: CatalogTool[] | null; error: string | null; used: { [tool: string]: number }; scopes: { [connectorId: string]: string | null }; noPrivateData?: string[] }

export type ContainerStats = { container: string; memories: number; documents: number; lastActivity: number | null }

export type AgentMemory =
  | { container: string; perChat: boolean; reads: string[]; chatContainers: number; connector: null; stats: null; error: null }
  | {
      container: string
      perChat: boolean
      reads: string[]
      chatContainers: number
      connector: { id: string; label: string; status: ConnectorStatus; enabled: boolean }
      stats: ContainerStats | null
      error: string | null
    }

export type SaveAgentInput = {
  name: string
  persona: string
  guestTools: GuestTools
  dailyTokenCap: number
  recallContainer: string | null
  recallReadContainers: string[]
  recallPerChat: boolean
  telegramConnectorId: string | null
  avatarImage: string | null
  avatarIcon: AgentIcon | null
  avatarColor: AgentColor | null
  // Left out keeps the agent's models; an empty role uses the global chain.
  modelRoles?: ModelRoles
  id?: string
  slug?: string
}

// Members of an agent (PLAN 19.4): people the owner shares it with.
export type MemberChannel = "whatsapp" | "telegram"
// address is the bare JID, LID or Telegram id; label is how to show it.
export type MemberAccount = { id: string; channel: MemberChannel; address: string; label: string }
export type MemberView = { id: string; name: string; accounts: MemberAccount[]; agents: { id: string; slug: string; name: string }[] }
export type Person = { channel: MemberChannel; address: string; label: string; name: string | null; lastAt: number }
// members: in this agent. others: in other agents. people: wrote in a chat, not a member yet.
export type AgentMembers = { members: MemberView[]; others: MemberView[]; people: Person[] }
export type SaveMemberInput = { agentId: string; id?: string; name: string; accounts: { channel: MemberChannel; address: string }[] }

export type ToolPermissionsInput = { id: string; allowAll?: boolean; tools?: { [tool: string]: ToolPermission | null } }

export const getAgentsPage = noInput<AgentsPage>("getAgentsPage", "GET")
export const getAgentPage = withInput<{ slug: string }, AgentPage>("getAgentPage", "GET")
export const getAgentToolsFn = withInput<{ id: string }, AgentTools>("getAgentToolsFn", "GET")
export const getAgentMemoryFn = withInput<{ id: string }, AgentMemory>("getAgentMemoryFn", "GET")
export const saveAgentFn = withInput<SaveAgentInput, { id: string; slug: string }>("saveAgentFn", "POST")
export const getAgentMembersFn = withInput<{ id: string }, AgentMembers>("getAgentMembersFn", "GET")
export const saveMemberFn = withInput<SaveMemberInput, { id: string }>("saveMemberFn", "POST")
export const setAgentMemberFn = withInput<{ agentId: string; memberId: string; on: boolean }, Ok>("setAgentMemberFn", "POST")
export const deleteAgentFn = withInput<{ id: string }, Ok>("deleteAgentFn", "POST")
export const setAgentToolPermissionsFn = withInput<ToolPermissionsInput, { toolPermissions: ToolPermissions }>("setAgentToolPermissionsFn", "POST")
// saved: false when the runtime was down, so the session wasn't saved to the old agent's memory first.
export const setChatAgentFn = withInput<{ chatId: string; agentId: string | null }, { saved: true } | { saved: false }>("setChatAgentFn", "POST")
export const setChatReplyModeFn = withInput<{ chatId: string; mode: ReplyMode }, Ok>("setChatReplyModeFn", "POST")
export const setChatAckFn = withInput<{ chatId: string; on?: boolean; emoji?: string }, Ok>("setChatAckFn", "POST")
export const setChatAllowedFn = withInput<{ chatId: string; allowed: boolean }, Ok>("setChatAllowedFn", "POST")
export const setChatDeployNoticeFn = withInput<{ chatId: string; on: boolean }, Ok>("setChatDeployNoticeFn", "POST")
