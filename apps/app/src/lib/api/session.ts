// Session, owner invite and passkey list. getSession, getInvite and
// acceptInvite are public (the login and invite pages call them signed out).
import { noInput, withInput } from "./client"
import type { Ok, PasskeyItem, Viewer } from "./types"

export type SessionState = { viewer: Viewer | null; configured: boolean; ownerExists: boolean }

export type InviteState = { ok: true; expiresAt: number; recovery: boolean } | { ok: false; problem: string }

export const getSession = noInput<SessionState>("getSession", "GET")
export const getInvite = withInput<{ token: string }, InviteState>("getInvite", "GET")
// Accepting the invite signs the owner in (the server sets the session cookie
// on this response). The page then asks for a passkey.
export const acceptInvite = withInput<{ token: string }, { ok: true; created: boolean }>("acceptInvite", "POST")
export const listPasskeys = noInput<PasskeyItem[]>("listPasskeys", "GET")
export const removePasskey = withInput<{ id: string }, Ok>("removePasskey", "POST")
