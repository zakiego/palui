import { describe, expect, it } from "vitest"
import { canonicalJson } from "./schema"
import { EXAMPLES, EXAMPLE_TOOLS } from "./test-helpers"
import { describeWriteCall, resolverFrom, sameWriteCalls, validateDefinition, type ValidateContext } from "./validate"

const ctx: ValidateContext = {
  resolveTool: resolverFrom(EXAMPLE_TOOLS),
  hookExists: (id) => id === "hk_github_palui",
  chatId: "chat_owner",
  chatAllowed: (id) => (id === "chat_owner" || id === "chat_web" ? "own" : id === "chat_family" ? "allowed" : false),
}

const base = (steps: unknown[], triggers: unknown[] = [{ id: "main", kind: "manual", start: (steps[0] as { id: string }).id }]) => ({ name: "T", triggers, steps })
const problems = (def: unknown) => validateDefinition(def, ctx).problems.map((p) => `${p.path}: ${p.message}`)

describe("the worked examples (RFC 9)", () => {
  for (const [name, def] of Object.entries(EXAMPLES)) {
    it(`${name} validates with no write calls`, () => {
      const r = validateDefinition(def, ctx)
      expect(r.problems).toEqual([])
      expect(r.ok).toBe(true)
      expect(r.writeCalls).toEqual([])
    })
  }

  it("fills defaults and records the resolved tools", () => {
    const r = validateDefinition(EXAMPLES.BRIEFING, ctx)
    expect(r.definition!.schema).toBe(1)
    expect(r.tools.weather).toMatchObject({ connectorId: "webfetch", tool: "ambil_halaman", class: "read", untrusted: true })
  })
})

describe("graph checks", () => {
  it("rejects unknown targets, duplicate ids and reserved ids", () => {
    expect(problems(base([{ id: "a", kind: "message", text: "x", next: "nope" }]))).toEqual(['steps[0].next: Step "a" points next to "nope", which isn\'t a step.'])
    expect(problems(base([{ id: "a", kind: "message", text: "x" }, { id: "a", kind: "message", text: "y" }]))[0]).toMatch(/used twice/)
    expect(problems(base([{ id: "fail", kind: "message", text: "x" }]))[0]).toMatch(/reserved/)
    expect(problems(base([{ id: "a", kind: "message", text: "x" }], [{ id: "t", kind: "manual", start: "zz" }]))[0]).toMatch(/isn't a step/)
  })

  it("rejects cycles and unreachable steps", () => {
    const loop = base([
      { id: "a", kind: "message", text: "x", next: "b" },
      { id: "b", kind: "if", when: "true", then: "a" },
    ])
    expect(problems(loop)[0]).toMatch(/loop: a -> b -> a/)
    const orphan = base([
      { id: "a", kind: "message", text: "x", next: null },
      { id: "b", kind: "message", text: "y" },
    ])
    expect(problems(orphan)).toEqual(['steps[1]: Step "b" can\'t be reached from any trigger. Point a next, branch or trigger start at it, or remove it.'])
  })

  it("an if uses then/else, and on.<answer> must close", () => {
    expect(problems(base([{ id: "a", kind: "if", when: "true", then: null, next: null }]))[0]).toMatch(/use then\/else/)
    const checkin = base([
      { id: "a", kind: "checkin", text: "?", deadline: { kind: "after", minutes: 60 }, on: { not_yet: "b" } },
      { id: "b", kind: "message", text: "x" },
    ])
    expect(problems(checkin)[0]).toMatch(/doesn't close/)
  })

  it("rejects nested each and item outside a body", () => {
    const nested = base([
      { id: "t", kind: "tool", tool: "mcp-keuangan__list_bills" },
      { id: "outer", kind: "each", items: "steps.t.data", body: "inner", next: null },
      { id: "inner", kind: "each", items: "steps.t.data", body: "m", next: null },
      { id: "m", kind: "message", text: "{{item}}" },
    ])
    expect(problems(nested).join("\n")).toMatch(/can't run inside another each/)
    const stray = base([{ id: "m", kind: "message", text: "{{item.name}}" }])
    expect(problems(stray)[0]).toMatch(/reads item\/index but isn't only inside an each body/)
  })
})

describe("expressions and templates", () => {
  it("reports parse errors with the path", () => {
    expect(problems(base([{ id: "a", kind: "if", when: "steps.x = 1", then: null }]))[0]).toMatch(/^steps\[0\]\.when: Use "=="/)
    expect(problems(base([{ id: "a", kind: "message", text: "{{steps.x | nope}}" }]))[0]).toMatch(/^steps\[0\]\.text: Unknown filter/)
    expect(problems(base([{ id: "a", kind: "set", values: { n: "flow.n +" } }]))[0]).toMatch(/^steps\[0\]\.values\.n:/)
  })

  it("keeps templates out of literal places", () => {
    expect(problems(base([{ id: "a", kind: "prompt", prompt: "Summarize {{trigger.payload}}" }]))[0]).toMatch(/prompt instruction is a literal/)
    expect(problems(base([{ id: "a", kind: "message", text: "x", to: "{{trigger.chat}}" }]))[0]).toMatch(/message.to is a literal/)
    expect(problems(base([{ id: "a", kind: "tool", tool: "reminders__daftar_pengingat", args: { "{{trigger.k}}": 1 } }]))[0]).toMatch(/Arg names are literals/)
    // Ids, kinds and targets are literals by shape.
    expect(problems(base([{ id: "{{trigger.x}}", kind: "message", text: "x" }])).some((x) => x.startsWith("steps[0].id:"))).toBe(true)
  })

  it("checks wait, chat commands, events and deadline next", () => {
    expect(problems(base([{ id: "w", kind: "wait" }]))[0]).toMatch(/exactly one of minutes or until/)
    expect(problems(base([{ id: "m", kind: "message", text: "x" }], [{ id: "c", kind: "chat", start: "m", command: "/status" }]))[0]).toMatch(/built-in command/)
    expect(problems(base([{ id: "m", kind: "message", text: "x" }], [{ id: "e", kind: "event", start: "m", event: "run.failed" }]))[0]).toMatch(/own run.failed/)
    const manualNext = base([{ id: "a", kind: "checkin", text: "?" }])
    expect(problems(manualNext)[0]).toMatch(/deadline "next"/)
  })
})

describe("tools and write calls", () => {
  it("refuses destructive tools and unknown tools", () => {
    expect(problems(base([{ id: "a", kind: "tool", tool: "reminders__batal_pengingat" }]))[0]).toMatch(/destructive. Flows never run destructive tools/)
    expect(problems(base([{ id: "a", kind: "tool", tool: "nope__x" }]))[0]).toMatch(/No enabled tool nope__x/)
    expect(problems(base([{ id: "a", kind: "prompt", prompt: "x", tools: ["reminders__batal_pengingat"] }]))[0]).toMatch(/never get destructive/)
  })

  it("resolves connectorId + tool and lists write calls with templates as written", () => {
    const r = validateDefinition(base([{ id: "a", kind: "tool", connectorId: "mcp-keuangan", tool: "mark_paid", args: { id: "{{trigger.id}}" } }]), ctx)
    expect(r.ok).toBe(true)
    expect(r.writeCalls).toEqual([{ stepId: "a", kind: "tool", connectorId: "mcp-keuangan", connectorType: "mcp", tool: "mark_paid", class: "write", args: { id: "{{trigger.id}}" } }])
    expect(describeWriteCall(r.writeCalls[0]!)).toBe("mcp-keuangan.mark_paid(id: {{trigger.id}})")
  })

  it("counts message.to an allowed chat as a write call, the owner's own chats as not", () => {
    const own = validateDefinition(base([{ id: "a", kind: "message", text: "x", to: "chat_web" }]), ctx)
    expect(own.writeCalls).toEqual([])
    const family = validateDefinition(base([{ id: "a", kind: "message", text: "x", to: "chat_family" }]), ctx)
    expect(family.writeCalls).toEqual([{ stepId: "a", kind: "message", to: "chat_family" }])
    expect(problems(base([{ id: "a", kind: "message", text: "x", to: "chat_stranger" }]))[0]).toMatch(/isn't one of the owner's chats/)
  })

  it("sameWriteCalls ignores order and spots changed args", () => {
    const a = [{ stepId: "x", kind: "tool" as const, connectorId: "c", tool: "t", class: "write" as const, args: { a: 1, b: "{{x}}" } }]
    expect(sameWriteCalls(a, [{ ...a[0]!, stepId: "renamed", args: { b: "{{x}}", a: 1 } }])).toBe(true)
    expect(sameWriteCalls(a, [{ ...a[0]!, args: { a: 2, b: "{{x}}" } }])).toBe(false)
  })
})

describe("limits", () => {
  it("caps steps and size", () => {
    const many = base(Array.from({ length: 41 }, (_, i) => ({ id: `s${i}`, kind: "message", text: "x" })))
    expect(problems(many)[0]).toMatch(/^steps:/)
    const big = base([{ id: "a", kind: "message", text: "x".repeat(4000) }, ...Array.from({ length: 9 }, (_, i) => ({ id: `b${i}`, kind: "message", text: "y".repeat(4000) }))])
    expect(problems(big)[0]).toMatch(/limit is 32 KB/)
  })

  it("hashes canonically", () => {
    expect(canonicalJson({ b: 1, a: { d: 1, c: 2 } })).toBe(canonicalJson({ a: { c: 2, d: 1 }, b: 1 }))
  })
})
