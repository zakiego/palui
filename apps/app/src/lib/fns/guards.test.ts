// Static guards over the dashboard server functions: every POST checks the
// origin, and reminder and job writes go through the bridges that poke the
// runtime's scheduler.
import { readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { describe, expect, it } from "vitest"

const dir = import.meta.dirname
const sources = readdirSync(dir)
  .filter((f) => f.endsWith(".ts") && !f.endsWith(".test.ts"))
  .map((f) => ({ file: f, text: readFileSync(join(dir, f), "utf8") }))

describe("dashboard server functions", () => {
  it("check the origin on every POST", () => {
    const missing: string[] = []
    for (const { file, text } of sources) {
      for (const chunk of text.split(/export const /).slice(1)) {
        if (!/createServerFn\(\{ method: "POST" \}\)/.test(chunk)) continue
        if (!/require(Writer|SameOrigin)\(\)/.test(chunk)) missing.push(`${file}: ${chunk.split(" ")[0]}`)
      }
    }
    expect(missing).toEqual([])
  })

  it("write reminders and jobs only through the bridges", () => {
    for (const { file, text } of sources) {
      expect(text, file).not.toMatch(/@palui\/core\/(reminders|jobs)\/service/)
    }
  })
})
