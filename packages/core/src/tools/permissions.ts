// Per-agent tool permissions (PLAN 19.4): the owner can let an agent run every
// tool without asking, or pick allow / ask / deny per tool. They only loosen
// turns the owner started (their own chats, or their message in a shared chat
// with owner powers). Jobs, flows, hooks and guests keep the class rules.
import type { ToolClass, ToolPermission, ToolPermissions } from "@palui/db"

export const TOOL_PERMISSIONS = ["allow", "ask", "deny"] as const
// Entries a policy keeps. Far more than an agent has tools.
export const MAX_TOOL_PERMISSIONS = 2000
// Namespaced tool names (collect.ts namespaced()).
export const TOOL_NAME = /^[A-Za-z0-9_-]{1,64}$/

export const NO_PERMISSIONS: ToolPermissions = { allowAll: false, tools: {} }

// A stored value, fixed up: unknown entries and malformed names are dropped.
export function readPermissions(raw: unknown): ToolPermissions {
  if (!raw || typeof raw !== "object") return NO_PERMISSIONS
  const r = raw as { allowAll?: unknown; tools?: unknown }
  const tools: Record<string, ToolPermission> = {}
  if (r.tools && typeof r.tools === "object") {
    for (const [name, p] of Object.entries(r.tools as Record<string, unknown>)) {
      if (TOOL_NAME.test(name) && (TOOL_PERMISSIONS as readonly unknown[]).includes(p)) tools[name] = p as ToolPermission
    }
  }
  return { allowAll: r.allowAll === true, tools }
}

// What the owner picked for one tool: its own entry, else allow when allowAll
// is on, else null (the class rule decides).
export function permissionFor(p: ToolPermissions | null | undefined, name: string): ToolPermission | null {
  const own = p?.tools?.[name]
  if (own) return own
  return p?.allowAll ? "allow" : null
}

// Names hidden from the agent in every turn.
export function deniedTools(p: ToolPermissions | null | undefined): string[] {
  return Object.entries(p?.tools ?? {})
    .filter(([, v]) => v === "deny")
    .map(([k]) => k)
}

export type ApprovalInput = {
  class: ToolClass
  permission: ToolPermission | null
  // The owner started this turn and is there to see it.
  ownerTurn: boolean
  tainted: boolean
  // Jobs, flows, hooks and shared chats: every write asks.
  strict: boolean
  // The call carries a saved file (a palui:attachment ref).
  sendsFile?: boolean
}

// Whether a call waits for the owner's yes.
export function needsApproval(i: ApprovalInput): boolean {
  if (i.permission === "ask") return true
  // A turn that read something untrusted never sends a saved file on its own:
  // injected text could pick the owner's KTP and an innocent-looking tool.
  if (i.sendsFile && i.tainted) return true
  if (i.permission === "allow" && i.ownerTurn) return false
  if (i.class === "destructive") return true
  return i.class === "write" && (i.tainted || i.strict)
}
