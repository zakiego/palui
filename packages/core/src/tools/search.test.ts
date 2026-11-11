import { describe, expect, it } from "vitest"
import { searchTools, tokens, type SearchDoc } from "./search"

const doc = (connectorId: string, connectorLabel: string, tool: string, description: string): SearchDoc => ({
  name: `${connectorId}__${tool}`,
  connectorId,
  connectorLabel,
  tool,
  description,
})

const docs = [
  doc("reminders", "Reminders", "ingatkan", "Buat pengingat pada waktu tertentu"),
  doc("mcp-jadwal-sholat", "jadwal-sholat", "search_cities", "Search Indonesian cities by name"),
  doc("mcp-jadwal-sholat", "jadwal-sholat", "get_prayer_times", "Get the prayer times for a city and a date"),
  doc("mcp-host", "host", "disk_free", "Free disk space on the host"),
]

describe("tool search", () => {
  it("tokenizes names, labels and prose alike", () => {
    expect(tokens("get_prayer_times")).toEqual(["get", "prayer", "times"])
    expect(tokens("Jadwal-Sholat")).toEqual(["jadwal", "sholat"])
  })

  it("matches by connector label in Indonesian and by description in English", () => {
    expect(searchTools(docs, "jadwal sholat", 5).map((h) => h.doc.tool)).toEqual(["search_cities", "get_prayer_times"])
    expect(searchTools(docs, "prayer time", 5)[0]!.doc.tool).toBe("get_prayer_times")
  })

  it("ranks a tool name hit above a description hit", () => {
    expect(searchTools(docs, "disk", 5)[0]!.doc.tool).toBe("disk_free")
  })

  it("returns nothing for an empty or unmatched query, and respects the limit", () => {
    expect(searchTools(docs, "", 5)).toEqual([])
    expect(searchTools(docs, "zzz", 5)).toEqual([])
    expect(searchTools(docs, "jadwal", 1)).toHaveLength(1)
  })
})
