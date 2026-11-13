// add_contact through a real turn: the engine, the approval prompt and the
// owner's answer, with the real outbound runtime.
import { eq } from "drizzle-orm"
import { describe, expect, it } from "vitest"
import { memoryDb, outbox, pendingApprovals, type DB } from "@palui/db"
import { callTool, channelRuntime, fakeChannel, harness, modelRuntime, say, scriptedModel, tgMessage } from "@palui/core/chat/testing"
import { claimOwner, createClaim } from "@palui/core/owner"
import { setSetting } from "@palui/core/settings"
import { getDefaultAgent, setToolPermissions } from "@palui/core/agents"
import { listContacts } from "@palui/core/outbound/service"
import runtime from "./runtime"

const ask = "tambah kontak Ana +62 822-0000-1002"
const args = { name: "Ana", phone: "+62 822-0000-1002" }

async function setup(steps: Parameters<typeof scriptedModel>[0], prep: (db: DB) => void = () => {}) {
  const db = memoryDb()
  const { nonce } = createClaim(db, "telegram", 1)
  claimOwner(db, { channel: "telegram", nonce, externalId: "42" }, 2)
  const links = [{ connectorId: "cliproxy", model: "m" }]
  setSetting(db, "modelRoles", { default: links, fast: links, vision: links })
  prep(db)
  const m = scriptedModel(steps)
  const h = await harness({
    db,
    registry: { cliproxy: modelRuntime("cliproxy", () => m.model), telegram: channelRuntime("telegram", fakeChannel("telegram")), outbound: runtime },
    rows: { cliproxy: "model", telegram: "channel", outbound: "skill" },
  })
  return h
}

describe("add_contact in a turn", () => {
  it("asks with the name and normalized number, and saves only after yes", async () => {
    const h = await setup([callTool("outbound__add_contact", args, "c1"), say("udah disimpan")])
    await h.engine.inbound(tgMessage(ask))
    await h.engine.idle()
    const a = h.db.select().from(pendingApprovals).get()!
    expect(a).toMatchObject({ tool: "outbound__add_contact", status: "pending", argsPreview: "Tambah kontak Ana (+62 822-0000-1002)\nPersetujuan kirim: selalu tanya" })
    expect(h.db.select().from(outbox).where(eq(outbox.source, `approval:${a.id}`)).get()!.payload.text).toContain("Tambah kontak Ana (+62 822-0000-1002)")
    expect(listContacts(h.db)).toHaveLength(0)

    await h.engine.inbound(tgMessage("", { callback: { data: `approve:${a.id}` } }))
    await h.engine.idle()
    expect(listContacts(h.db)).toMatchObject([{ name: "Ana", address: "6282200001002", policy: "ask", agentId: getDefaultAgent(h.db).id }])
  })

  it("a rejection leaves nothing saved", async () => {
    const h = await setup([callTool("outbound__add_contact", args, "c1"), say("oke, gak jadi")])
    await h.engine.inbound(tgMessage(ask))
    await h.engine.idle()
    const a = h.db.select().from(pendingApprovals).get()!
    await h.engine.inbound(tgMessage("", { callback: { data: `deny:${a.id}` } }))
    await h.engine.idle()
    expect(h.db.select().from(pendingApprovals).get()!.status).toBe("denied")
    expect(listContacts(h.db)).toHaveLength(0)
  })

  it("still asks when the agent may run every tool, or this tool, without asking", async () => {
    const h = await setup([callTool("outbound__add_contact", args, "c1"), say("x")], (db) =>
      setToolPermissions(db, getDefaultAgent(db).id, { allowAll: true, tools: { outbound__add_contact: "allow" } }),
    )
    await h.engine.inbound(tgMessage(ask))
    await h.engine.idle()
    expect(h.db.select().from(pendingApprovals).get()).toMatchObject({ tool: "outbound__add_contact", status: "pending" })
    expect(listContacts(h.db)).toHaveLength(0)
  })
})
