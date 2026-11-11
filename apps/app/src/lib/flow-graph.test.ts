import { describe, expect, it } from "vitest"
import { layoutFlow, stepKey, triggerKey, type GraphStep, type LaidNode } from "./flow-graph"

const step = (id: string, edges: GraphStep["edges"], ends = false): GraphStep => ({ id, edges, ends })
const node = (nodes: LaidNode[], key: string) => nodes.find((n) => n.key === key)!
const overlap = (a: LaidNode, b: LaidNode) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h

describe("layoutFlow", () => {
  it("stacks a straight flow in one column under its trigger, with an end below", () => {
    const { nodes, edges } = layoutFlow(
      [step("a", [{ label: "next", to: "b" }]), step("b", [{ label: "next", to: "c" }]), step("c", [], true)],
      [{ id: "daily", start: "a" }],
    )
    const t = node(nodes, triggerKey("a"))
    const [a, b, c] = ["a", "b", "c"].map((id) => node(nodes, stepKey(id)))
    expect([t.rank, a!.rank, b!.rank, c!.rank]).toEqual([0, 1, 2, 3])
    expect(new Set([t, a!, b!, c!].map((n) => n.x + n.w / 2)).size).toBe(1)
    const end = nodes.filter((n) => n.kind === "end")
    expect(end).toHaveLength(1)
    expect(end[0]!.rank).toBe(4)
    // next edges carry no label; the trigger edge neither.
    expect(edges.every((e) => e.label === "")).toBe(true)
    expect(edges).toHaveLength(4)
  })

  it("splits an if into two columns and joins them again", () => {
    const { nodes, edges } = layoutFlow(
      [
        step("check", [
          { label: "then", to: "yes" },
          { label: "else", to: "no" },
        ]),
        step("yes", [{ label: "next", to: "done" }]),
        step("no", [{ label: "next", to: "done" }]),
        step("done", [], true),
      ],
      [{ id: "t", start: "check" }],
    )
    const [check, yes, no, done] = ["check", "yes", "no", "done"].map((id) => node(nodes, stepKey(id))!)
    expect(yes!.rank).toBe(no!.rank)
    expect(yes!.x).not.toBe(no!.x)
    expect(done!.rank).toBe(yes!.rank + 1)
    // The join sits under the middle of the branch, like the if above it.
    expect(done!.x).toBeCloseTo(check!.x)
    expect(edges.map((e) => e.label).sort()).toEqual(["", "", "", "", "else", "then"])
  })

  it("gives every way to finish its own end, labelled like the branch", () => {
    const { nodes, edges } = layoutFlow(
      [
        step("check", [
          { label: "then", to: "send" },
          { label: "else", to: null },
        ]),
        step("send", [], true),
      ],
      [],
    )
    expect(nodes.filter((n) => n.kind === "end")).toHaveLength(2)
    expect(edges.find((e) => e.to.startsWith("e:check"))?.label).toBe("else")
    // No triggers: the first step is the root.
    expect(node(nodes, stepKey("check")).rank).toBe(0)
  })

  it("routes a long edge around the step it would cross", () => {
    // check -> a -> b -> c, and check's else jumps straight to c, past a and b.
    const { nodes, edges } = layoutFlow(
      [
        step("check", [
          { label: "then", to: "a" },
          { label: "else", to: "c" },
        ]),
        step("a", [{ label: "next", to: "b" }]),
        step("b", [{ label: "next", to: "c" }]),
        step("c", [], true),
      ],
      [{ id: "t", start: "check" }],
    )
    const skip = edges.find((e) => e.label === "else")!
    expect(skip.path).toContain(" V ")
    const a = node(nodes, stepKey("a"))
    const lane = Number(/ V [\d.]+ C ([\d.-]+)/.exec(skip.path)?.[1])
    expect(lane < a.x || lane > a.x + a.w).toBe(true)
  })

  it("never overlaps nodes, and keeps everything inside the box", () => {
    const steps: GraphStep[] = [
      step("ask", [
        { label: "next", to: "wrap" },
        { label: "on.yes", to: "y" },
        { label: "on.no", to: "n" },
        { label: "onMissed", to: "m" },
      ]),
      step("y", [{ label: "next", to: "wrap" }]),
      step("n", [{ label: "next", to: "wrap" }]),
      step("m", [{ label: "next", to: null }]),
      step("wrap", [{ label: "onError", to: "oops" }], true),
      step("oops", [], true),
    ]
    const { nodes, width, height } = layoutFlow(steps, [
      { id: "morning", start: "ask" },
      { id: "hook", start: "ask" },
    ])
    for (const a of nodes) {
      expect(a.x).toBeGreaterThanOrEqual(0)
      expect(a.x + a.w).toBeLessThanOrEqual(width)
      expect(a.y + a.h).toBeLessThanOrEqual(height)
      for (const b of nodes) if (a !== b) expect(overlap(a, b)).toBe(false)
    }
  })

  it("draws triggers that start at the same step as one node", () => {
    const { nodes } = layoutFlow([step("a", [], true), step("b", [], true)], [
      { id: "morning", start: "a" },
      { id: "evening", start: "a" },
      { id: "hook", start: "b" },
    ])
    expect(nodes.filter((n) => n.kind === "trigger").map((n) => n.ref).sort()).toEqual(["a", "b"])
  })

  it("skips edges and triggers that point at a missing step", () => {
    const { nodes, edges } = layoutFlow([step("a", [{ label: "next", to: "ghost" }], true)], [{ id: "t", start: "ghost" }])
    expect(nodes.map((n) => n.kind).sort()).toEqual(["end", "step"])
    expect(edges).toHaveLength(1)
  })
})
