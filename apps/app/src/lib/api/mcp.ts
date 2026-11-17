// MCP access: who may call Palui's own MCP server (/mcp). Personal tokens and
// OAuth connections are both grants; the OAuth consent page answers requests
// an app started at /api/oauth/authorize.
import { noInput, withInput } from "./client"
import type { Ok } from "./types"

export type McpGrant = {
  id: string
  kind: "token" | "oauth"
  name: string
  // Last four characters of a personal token; null for OAuth connections.
  tokenHint: string | null
  clientUri: string | null
  createdAt: number
  lastUsedAt: number | null
  // When an OAuth connection lapses unless the app refreshes it.
  expiresAt: number | null
}

export type McpAccess = { url: string; grants: McpGrant[] }

export type OAuthRequest = {
  id: string
  clientName: string
  clientUri: string | null
  redirectHost: string
  scope: string | null
  expiresAt: number
}

export const getMcpAccess = noInput<McpAccess>("getMcpAccess", "GET")
// The token is in this answer only. The list shows its last four characters.
export const createMcpToken = withInput<{ name: string }, { token: string; grant: McpGrant }>("createMcpToken", "POST")
export const revokeMcpAccess = withInput<{ id: string }, Ok>("revokeMcpAccess", "POST")
export const getOAuthRequest = withInput<{ id: string }, OAuthRequest>("getOAuthRequest", "GET")
export const decideOAuthRequest = withInput<{ id: string; allow: boolean }, { redirectTo: string }>("decideOAuthRequest", "POST")
