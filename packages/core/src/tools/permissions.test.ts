import { describe, expect, it } from "vitest"
import { deniedTools, needsApproval, permissionFor, readPermissions } from "./permissions"

describe("tool permissions", () => {
  it("reads a stored value and drops what it doesn't know", () => {
    expect(readPermissions(null)).toEqual({ allowAll: false, tools: {} })
    expect(readPermissions({ allowAll: true, tools: { a__x: "allow", "bad name": "allow", a__y: "maybe", a__z: "deny" } })).toEqual({ allowAll: true, tools: { a__x: "allow", a__z: "deny" } })
  })

  it("a tool's own entry wins over allow-all", () => {
    const p = { allowAll: true, tools: { a__x: "ask" as const } }
    expect(permissionFor(p, "a__x")).toBe("ask")
    expect(permissionFor(p, "a__y")).toBe("allow")
    expect(permissionFor({ allowAll: false, tools: {} }, "a__y")).toBeNull()
    expect(permissionFor(null, "a__y")).toBeNull()
  })

  it("lists denied tools", () => {
    expect(deniedTools({ allowAll: false, tools: { a__x: "deny", a__y: "allow" } })).toEqual(["a__x"])
    expect(deniedTools(null)).toEqual([])
  })

  it("allow skips the prompt only in a turn the owner started", () => {
    const base = { class: "destructive" as const, tainted: false, strict: false }
    expect(needsApproval({ ...base, permission: null, ownerTurn: true })).toBe(true)
    expect(needsApproval({ ...base, permission: "allow", ownerTurn: true })).toBe(false)
    expect(needsApproval({ ...base, permission: "allow", ownerTurn: false })).toBe(true)
    // A write in a tainted or strict owner turn still runs when allowed.
    expect(needsApproval({ class: "write", permission: "allow", ownerTurn: true, tainted: true, strict: true })).toBe(false)
    expect(needsApproval({ class: "write", permission: "allow", ownerTurn: false, tainted: false, strict: true })).toBe(true)
  })

  it("ask always asks, even for a read", () => {
    expect(needsApproval({ class: "read", permission: "ask", ownerTurn: true, tainted: false, strict: false })).toBe(true)
  })

  it("no entry keeps the class rule", () => {
    const i = { permission: null, ownerTurn: true }
    expect(needsApproval({ ...i, class: "read", tainted: true, strict: true })).toBe(false)
    expect(needsApproval({ ...i, class: "write", tainted: false, strict: false })).toBe(false)
    expect(needsApproval({ ...i, class: "write", tainted: true, strict: false })).toBe(true)
  })
})
