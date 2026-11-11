import { describe, expect, it } from "vitest"
import { ArgError, buildArgv, parseConfig, type CommandSpec } from "./config.ts"

const TOKEN = "x".repeat(32)

function command(c: Record<string, unknown>): CommandSpec {
  return parseConfig({ token: TOKEN, commands: [{ name: "t", description: "t", class: "read", command: "/bin/echo", ...c }] }).commands[0]!
}

describe("parseConfig", () => {
  it("applies defaults", () => {
    const c = parseConfig({ token: TOKEN })
    expect(c).toMatchObject({ host: "127.0.0.1", port: 4893, commands: [] })
  })

  it("rejects a short token, relative commands and duplicate names", () => {
    expect(() => parseConfig({ token: "short" })).toThrow(/32 characters/)
    expect(() => command({ command: "echo" })).toThrow(/absolute path/)
    expect(() =>
      parseConfig({
        token: TOKEN,
        commands: [
          { name: "a", description: "a", class: "read", command: "/bin/echo" },
          { name: "a", description: "a", class: "read", command: "/bin/echo" },
        ],
      }),
    ).toThrow(/duplicate/)
  })

  it("requires every placeholder to be declared and every param to be used", () => {
    expect(() => command({ args: ["{x}"] })).toThrow(/undeclared param/)
    expect(() => command({ params: { x: { pattern: "\\w+" } } })).toThrow(/isn't used/)
    expect(() => command({ args: ["{x}"], params: { x: {} } })).toThrow(/exactly one of enum or pattern/)
    expect(() => command({ args: ["{x}"], params: { x: { pattern: "(" } } })).toThrow(/valid regular expression/)
  })

  it("keeps list params whole-arg only", () => {
    expect(() => command({ args: ["a{x}"], params: { x: { pattern: "\\w+", list: true } } })).toThrow(/whole arg/)
    expect(() => command({ args: ["{...x}"], params: { x: { pattern: "\\w+" } } })).toThrow(/needs a list param/)
  })

  it("names the bad key instead of ignoring a typo", () => {
    expect(() => command({ timeout: 5000 })).toThrow(/command "t": Unrecognized key: "timeout"/)
    expect(() => command({ args: ["{x}"], params: { x: { pattern: "\\w+", allowdash: true } } })).toThrow(/command "t"\.params\.x: Unrecognized key: "allowdash"/)
    expect(() => parseConfig({ token: TOKEN, prot: 1 })).toThrow(/Unrecognized key: "prot"/)
  })

  it("accepts notes and a title", () => {
    const c = command({ title: "Echo on the host", note: "kept for testing" })
    expect(c.title).toBe("Echo on the host")
  })

  it("points at a nameless command by index", () => {
    expect(() => parseConfig({ token: TOKEN, commands: [{ description: "x", class: "read", command: "/bin/echo" }] })).toThrow(/commands\[0\]\.name/)
  })

  it("expands ~ in command and cwd", () => {
    const c = command({ command: "~/bin/tool", cwd: "~/work" })
    expect(c.command).not.toContain("~")
    expect(c.cwd).not.toContain("~")
  })
})

describe("buildArgv", () => {
  const spec = command({
    args: ["-C", "{repo}", "log", "-n", "{count}", "--format={fmt}", "{...paths}"],
    params: {
      repo: { pattern: "[\\w./-]+" },
      count: { pattern: "\\d{1,3}", optional: true },
      fmt: { enum: ["oneline", "short"] },
      paths: { pattern: "[\\w./]+", list: true, optional: true },
    },
  })

  it("substitutes values and spreads lists", () => {
    expect(buildArgv(spec, { repo: "palui", count: "5", fmt: "oneline", paths: ["a.ts", "b.ts"] })).toEqual([
      "-C",
      "palui",
      "log",
      "-n",
      "5",
      "--format=oneline",
      "a.ts",
      "b.ts",
    ])
  })

  it("drops an arg whose optional param is absent", () => {
    expect(buildArgv(spec, { repo: "palui", fmt: "short" })).toEqual(["-C", "palui", "log", "-n", "--format=short"])
  })

  it("refuses unknown, missing, mismatched and flag-like values", () => {
    expect(() => buildArgv(spec, { repo: "p", fmt: "short", extra: "1" })).toThrow(ArgError)
    expect(() => buildArgv(spec, { fmt: "short" })).toThrow(/missing param "repo"/)
    expect(() => buildArgv(spec, { repo: "p", fmt: "full" })).toThrow(/one of/)
    expect(() => buildArgv(spec, { repo: "a b", fmt: "short" })).toThrow(/doesn't match/)
    expect(() => buildArgv(spec, { repo: "--upload-pack=x", fmt: "short" })).toThrow(/can't start with "-"/)
    expect(() => buildArgv(spec, { repo: "p", fmt: "short", paths: ["ok", "-rf"] })).toThrow(/can't start with "-"/)
    expect(() => buildArgv(spec, { repo: "p", fmt: "short", paths: "a.ts" })).toThrow(/list of strings/)
  })

  it("anchors patterns so a partial match isn't enough", () => {
    expect(() => buildArgv(spec, { repo: "p", count: "12; rm", fmt: "short" })).toThrow(/doesn't match/)
  })

  it("never allows control characters, even with a loose pattern", () => {
    const loose = command({ args: ["{v}"], params: { v: { pattern: "[\\s\\S]*", allowDash: true } } })
    expect(buildArgv(loose, { v: "-x" })).toEqual(["-x"])
    expect(() => buildArgv(loose, { v: "a\nb" })).toThrow(/control characters/)
  })
})
