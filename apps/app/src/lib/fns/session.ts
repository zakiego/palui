// Session, owner invite and passkey server functions.
import { createServerFn } from "@tanstack/react-start"
import { getRequest } from "@tanstack/react-start/server"
import { z } from "zod"
import { authConfigured, getAuth } from "@/server/better-auth"
import { getDb } from "@/server/db"
import { acceptOwnerInvite, InviteError, inspectInvite, ownerExists } from "@/server/owner-invite"
import { deletePasskey, passkeyList } from "@/server/queries"
import { getViewer, requireUser, requireWriter, requireSameOrigin } from "@/server/session"

const token = z.string().min(10).max(200)

export const getSession = createServerFn({ method: "GET" }).handler(async () => {
  const viewer = await getViewer()
  return { viewer, configured: authConfigured(), ownerExists: ownerExists(getDb()) }
})

export const getInvite = createServerFn({ method: "GET" })
  .validator(z.object({ token }))
  .handler(async ({ data }) => inspectInvite(getDb(), data.token))

// Accepting the invite signs in server side: the cookie plugin writes the
// session cookie on this response. The page then asks for a passkey.
export const acceptInvite = createServerFn({ method: "POST" })
  .validator(z.object({ token }))
  .handler(async ({ data }) => {
    requireSameOrigin()
    const db = getDb()
    let res: Awaited<ReturnType<typeof acceptOwnerInvite>>
    try {
      res = await acceptOwnerInvite(db, data.token)
    } catch (err) {
      if (err instanceof InviteError) throw new Error(err.message)
      throw err
    }
    try {
      await getAuth(db).api.signInUsername({ body: { username: res.username, password: res.password }, headers: getRequest().headers })
    } catch {
      // The link is used up by now, so the only way forward is a new one.
      throw new Error("Signing in failed after the link was used. Run `palui owner invite` on the server for a new link.")
    }
    return { ok: true, created: res.created }
  })

export const listPasskeys = createServerFn({ method: "GET" }).handler(async () => {
  const { db, viewer } = await requireUser()
  return passkeyList(db, viewer.user.id)
})

export const removePasskey = createServerFn({ method: "POST" })
  .validator(z.object({ id: z.string().min(1).max(200) }))
  .handler(async ({ data }) => {
    const { db, viewer } = await requireWriter()
    const all = passkeyList(db, viewer.user.id)
    // Removing the last passkey would lock the owner out of everything but the CLI.
    if (all.length <= 1) throw new Error("This is your only passkey. Add another one before removing it.")
    if (!deletePasskey(db, viewer.user.id, data.id)) throw new Error("No such passkey.")
    return { ok: true }
  })
