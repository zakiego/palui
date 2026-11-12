import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { describe, expect, it } from "vitest"
import { loadBuildInfo, parseBuildInfo, subjectsSince } from "./build-info"

const A = "a".repeat(40)
const B = "b".repeat(40)
const C = "c".repeat(40)

describe("loadBuildInfo", () => {
  it("reads the file the deploy wrote", () => {
    const dir = mkdtempSync(join(tmpdir(), "palui-build-"))
    const path = join(dir, "build-info.json")
    writeFileSync(path, JSON.stringify({ commit: A, history: [{ sha: A, subjects: ["x"] }] }))
    expect(loadBuildInfo(path)).toEqual({ commit: A, history: [{ sha: A, subjects: ["x"] }] })
  })

  it("returns null for no path, a missing file or bad content", () => {
    const dir = mkdtempSync(join(tmpdir(), "palui-build-"))
    expect(loadBuildInfo(undefined)).toBeNull()
    expect(loadBuildInfo(join(dir, "nope.json"))).toBeNull()
    const bad = join(dir, "bad.json")
    writeFileSync(bad, "{not json")
    expect(loadBuildInfo(bad)).toBeNull()
    expect(parseBuildInfo({ commit: "" })).toBeNull()
    expect(parseBuildInfo({ commit: "not-a-sha" })).toBeNull()
    expect(parseBuildInfo({ commit: A })).toEqual({ commit: A, history: [] })
  })
})

describe("subjectsSince", () => {
  const info = { commit: C, history: [{ sha: C, subjects: ["c1", "c2"] }, { sha: B, subjects: ["b"] }, { sha: A, subjects: ["a"] }] }
  it("lists what came after the previous commit", () => {
    expect(subjectsSince(info, A)).toEqual(["c1", "c2", "b"])
    expect(subjectsSince(info, B)).toEqual(["c1", "c2"])
  })
  it("shows only the head when the previous commit is unknown", () => {
    expect(subjectsSince(info, null)).toEqual(["c1", "c2"])
    expect(subjectsSince(info, "d".repeat(40))).toEqual(["c1", "c2"])
  })
})
