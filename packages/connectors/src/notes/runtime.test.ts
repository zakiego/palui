import { describe, expect, it } from "vitest"
import { memoryDb } from "@palui/db"
import type { PaluiTool } from "@palui/core/connectors/define"
import { listInstructions, notesPrompt } from "@palui/core/notes/service"
import { notesTools } from "./runtime"

// biome-ignore lint: test helper calls the tool with loose input.
async function call(t: PaluiTool | undefined, input: any): Promise<any> {
  const exec = t?.tool.execute
  if (!exec) throw new Error("tool has no execute")
  return exec(input, { toolCallId: "tc_1", messages: [] } as never)
}

describe("notes tools", () => {
  it("classes: instruction writes are destructive, preference writes are write", () => {
    const tools = notesTools(memoryDb())
    expect(Object.fromEntries(Object.entries(tools).map(([k, t]) => [k, t.class]))).toEqual({
      simpan_instruksi: "destructive",
      daftar_instruksi: "read",
      hapus_instruksi: "destructive",
      simpan_preferensi: "write",
      lihat_preferensi: "read",
      hapus_preferensi: "write",
    })
  })

  it("saves, updates and deletes an instruction", async () => {
    const db = memoryDb()
    const tools = notesTools(db, () => 1000)
    const saved = await call(tools.simpan_instruksi, { title: "Jadwal sholat", body: "Pakai mcp-jadwal-sholat." })
    expect(saved.ok).toBe(true)
    const updated = await call(tools.simpan_instruksi, { id: saved.instruction.id, title: "Jadwal sholat", body: "Pakai API v1." })
    expect(updated.message).toContain("diperbarui")
    expect(listInstructions(db).map((r) => r.body)).toEqual(["Pakai API v1."])
    expect((await call(tools.hapus_instruksi, { id: saved.instruction.id })).ok).toBe(true)
    expect((await call(tools.hapus_instruksi, { id: saved.instruction.id })).ok).toBe(false)
  })

  it("sets and reads preferences, which show up in the prompt", async () => {
    const db = memoryDb()
    const tools = notesTools(db)
    expect((await call(tools.simpan_preferensi, { key: "default_city", value: "Banjarmasin" })).ok).toBe(true)
    expect((await call(tools.lihat_preferensi, {})).preferences).toEqual({ default_city: "Banjarmasin" })
    expect(notesPrompt(db)).toContain("default_city: Banjarmasin")
    expect((await call(tools.simpan_preferensi, { key: "bad key!", value: "x" })).ok).toBe(false)
    expect((await call(tools.hapus_preferensi, { key: "default_city" })).ok).toBe(true)
    expect((await call(tools.hapus_preferensi, { key: "default_city" })).ok).toBe(false)
  })
})
