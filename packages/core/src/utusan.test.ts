import { mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { checkUtusan, utusanBindsChat } from "./utusan"

const OWNER_JID = "628123456789@s.whatsapp.net"
let dataDir: string

beforeEach(() => {
  dataDir = mkdtempSync(join(tmpdir(), "palui-utusan-"))
})

afterEach(() => {
  rmSync(dataDir, { recursive: true, force: true })
})

describe("utusanBindsChat", () => {
  function file(content: unknown): string {
    const p = join(dataDir, "utusan.json")
    writeFileSync(p, typeof content === "string" ? content : JSON.stringify(content))
    return p
  }

  it("finds whatsapp bindings by jid", () => {
    const p = file({
      bindings: [
        { id: "dm", channel: "whatsapp", peer: { kind: "direct", id: OWNER_JID }, agent: "personal" },
        { id: "vorneo", channel: "whatsapp", peer: { kind: "group", id: "120363@g.us" }, agent: "vorneo" },
        { id: "stub", channel: "whatsapp", peer: { kind: "direct", id: "628777" }, stub: true },
        { id: "web", channel: "web", peer: { kind: "direct", id: "629999@s.whatsapp.net" } },
      ],
    })
    expect(utusanBindsChat(OWNER_JID, p)).toBe("bound")
    expect(utusanBindsChat("120363@g.us", p)).toBe("bound")
    expect(utusanBindsChat("628777@s.whatsapp.net", p)).toBe("bound")
    expect(utusanBindsChat("629999@s.whatsapp.net", p)).toBe("free")
    expect(utusanBindsChat("628123456789@g.us", p)).toBe("free")
  })

  it("is unknown when the file is missing or broken", () => {
    expect(utusanBindsChat(OWNER_JID, join(dataDir, "missing.json"))).toBe("unknown")
    expect(utusanBindsChat(OWNER_JID, file("{ not json"))).toBe("unknown")
    expect(utusanBindsChat(OWNER_JID, file({ bindings: "nope" }))).toBe("unknown")
    expect(utusanBindsChat(OWNER_JID, file({ principals: [] }))).toBe("free")
  })

  it("checks both the LID and the phone JID of a chat", () => {
    const p = file({ bindings: [{ id: "dm", channel: "whatsapp", peer: { kind: "direct", id: OWNER_JID } }] })
    // A DM arriving as a LID: the LID alone reads free, with its phone form it's bound.
    expect(utusanBindsChat("555@lid", p)).toBe("free")
    expect(utusanBindsChat(["555@lid", OWNER_JID], p)).toBe("bound")
    expect(checkUtusan(["555@lid", OWNER_JID], p)).toEqual({ state: "bound", bindingId: "dm" })
    const lidBound = file({ bindings: [{ id: "lid", channel: "whatsapp", peer: { kind: "direct", id: "555@lid" } }] })
    expect(utusanBindsChat([OWNER_JID, "555@lid"], lidBound)).toBe("bound")
    expect(utusanBindsChat([OWNER_JID, undefined], lidBound)).toBe("free")
  })
})
