import { describe, expect, it } from "vitest"

import { checkCommand, normalizeName } from "./commands"

const builtins = ["/report", "/help", "/start"]

describe("custom command form", () => {
  it("normalizes a typed name", () => {
    expect(normalizeName("  /Report-Fix ")).toBe("report-fix")
  })

  it("accepts a valid command", () => {
    expect(checkCommand({ name: "/report-fix", prompt: "Fix {{args}}" }, builtins)).toEqual({})
  })

  it("refuses built-ins, bad names and an empty prompt", () => {
    expect(checkCommand({ name: "/report", prompt: "x" }, builtins).name).toEqual({ key: "commands.err.builtin", name: "/report" })
    expect(checkCommand({ name: "bad name", prompt: "x" }, builtins).name?.key).toBe("commands.err.name")
    expect(checkCommand({ name: "-x", prompt: "x" }, builtins).name?.key).toBe("commands.err.name")
    expect(checkCommand({ name: "a".repeat(33), prompt: "x" }, builtins).name?.key).toBe("commands.err.name")
    expect(checkCommand({ name: "/", prompt: "  " }, builtins)).toEqual({ name: { key: "commands.err.nameEmpty" }, prompt: { key: "commands.err.prompt" } })
  })
})
