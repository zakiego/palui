import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import { buildSystem } from "../chat/context"
import {
  createInstruction,
  deleteInstruction,
  deletePreference,
  getPreference,
  listInstructions,
  listPreferences,
  MAX_INSTRUCTIONS,
  notesPrompt,
  NotesError,
  setPreference,
  updateInstruction,
} from "./service"

const NOW = Date.UTC(2026, 10, 11, 2, 0)

describe("instructions", () => {
  it("creates, updates, lists and deletes", () => {
    const db = memoryDb()
    const a = createInstruction(db, { title: "Jadwal sholat", body: "Pakai server jadwal-sholat.\n\n\n\nAtau API-nya." }, NOW)
    expect(a.body).toBe("Pakai server jadwal-sholat.\n\nAtau API-nya.")
    const b = updateInstruction(db, a.id, { body: "Pakai mcp-jadwal-sholat." }, NOW + 1)
    expect(b).toMatchObject({ title: "Jadwal sholat", body: "Pakai mcp-jadwal-sholat.", updatedAt: NOW + 1 })
    expect(listInstructions(db)).toHaveLength(1)
    expect(deleteInstruction(db, a.id)).toBe(true)
    expect(deleteInstruction(db, a.id)).toBe(false)
  })

  it("refuses empty, oversized and too many", () => {
    const db = memoryDb()
    expect(() => createInstruction(db, { title: " ", body: "x" }, NOW)).toThrow(NotesError)
    expect(() => createInstruction(db, { title: "t", body: "x".repeat(1001) }, NOW)).toThrow(NotesError)
    for (let i = 0; i < MAX_INSTRUCTIONS; i++) createInstruction(db, { title: `t${i}`, body: "x" }, NOW)
    expect(() => createInstruction(db, { title: "one more", body: "x" }, NOW)).toThrow(/20 instruksi/)
    expect(() => updateInstruction(db, "ins_missing", { body: "x" }, NOW)).toThrow(NotesError)
  })
})

describe("preferences", () => {
  it("normalizes keys and upserts", () => {
    const db = memoryDb()
    setPreference(db, "Default City", "Banjarmasin", NOW)
    setPreference(db, "default_city", "Martapura", NOW + 1)
    expect(getPreference(db, "default-city")).toBe("Martapura")
    expect(listPreferences(db)).toHaveLength(1)
    expect(deletePreference(db, "DEFAULT_CITY")).toBe(true)
    expect(getPreference(db, "default_city")).toBeUndefined()
  })

  it("refuses bad keys and empty or long values", () => {
    const db = memoryDb()
    expect(() => setPreference(db, "1city", "x", NOW)).toThrow(NotesError)
    expect(() => setPreference(db, "city!", "x", NOW)).toThrow(NotesError)
    expect(() => setPreference(db, "city", "  ", NOW)).toThrow(NotesError)
    expect(() => setPreference(db, "city", "x".repeat(301), NOW)).toThrow(NotesError)
  })
})

describe("notesPrompt", () => {
  it("is null with nothing saved", () => {
    expect(notesPrompt(memoryDb())).toBeNull()
  })

  it("lists enabled instructions and every preference, and lands in the system prompt", () => {
    const db = memoryDb()
    createInstruction(db, { title: "Jadwal sholat", body: "Pakai mcp-jadwal-sholat." }, NOW)
    createInstruction(db, { title: "Off", body: "hidden", enabled: false }, NOW)
    setPreference(db, "default_city", "Banjarmasin", NOW)
    const notes = notesPrompt(db)!
    expect(notes).toContain("- Jadwal sholat: Pakai mcp-jadwal-sholat.")
    expect(notes).toContain("- default_city: Banjarmasin")
    expect(notes).not.toContain("hidden")
    const system = buildSystem({ persona: "P", channel: "telegram", capabilities: [], notes, trigger: "message" })
    expect(system).toContain("Standing instructions from the owner")
    expect(system).toContain("Owner preferences")
  })
})
