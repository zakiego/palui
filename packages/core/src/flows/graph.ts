// The step graph of a flow definition: edges, the default `next`, reachability
// and cycle detection. Pure, shared by the validator and the engine.
import type { FlowDefinition, FlowStep } from "./schema"

export type Edge = { to: string; label: string; body?: boolean }

// The step after `step` when it finishes normally: its `next`, or the next one
// in the array, or null at the end. `if` steps only use then/else.
export function defaultNext(def: Pick<FlowDefinition, "steps">, stepId: string): string | null {
  const i = def.steps.findIndex((s) => s.id === stepId)
  if (i < 0) return null
  const s = def.steps[i]!
  if (s.next !== undefined) return s.next
  return def.steps[i + 1]?.id ?? null
}

export function edgesOf(def: Pick<FlowDefinition, "steps">, s: FlowStep): Edge[] {
  const out: Edge[] = []
  const add = (to: string | null | undefined, label: string, body = false) => {
    if (to) out.push({ to, label, ...(body ? { body } : {}) })
  }
  if (s.kind === "if") {
    add(s.then, "then")
    add(s.else, "else")
  } else {
    add(defaultNext(def, s.id), "next")
  }
  if (s.kind === "checkin") {
    for (const [answer, to] of Object.entries(s.on ?? {})) add(to, `on.${answer}`)
    add(s.onMissed ?? undefined, "onMissed")
  }
  if (s.kind === "each") add(s.body, "body", true)
  if (s.onError && s.onError !== "fail" && s.onError !== "continue") add(s.onError, "onError")
  return out
}

export function stepMap(def: Pick<FlowDefinition, "steps">): Map<string, FlowStep> {
  return new Map(def.steps.map((s) => [s.id, s]))
}

// Steps reachable from `starts`. `viaBody: false` stops at each.body edges.
export function reachable(def: Pick<FlowDefinition, "steps">, starts: string[], viaBody = true): Set<string> {
  const byId = stepMap(def)
  const seen = new Set<string>()
  const stack = [...starts]
  while (stack.length) {
    const id = stack.pop()!
    if (seen.has(id) || !byId.has(id)) continue
    seen.add(id)
    for (const e of edgesOf(def, byId.get(id)!)) if (viaBody || !e.body) stack.push(e.to)
  }
  return seen
}

// A cycle as a list of step ids, or null.
export function findCycle(def: Pick<FlowDefinition, "steps">): string[] | null {
  const byId = stepMap(def)
  const state = new Map<string, 1 | 2>()
  const path: string[] = []
  const visit = (id: string): string[] | null => {
    const st = state.get(id)
    if (st === 2) return null
    if (st === 1) return [...path.slice(path.indexOf(id)), id]
    const s = byId.get(id)
    if (!s) return null
    state.set(id, 1)
    path.push(id)
    for (const e of edgesOf(def, s)) {
      const c = visit(e.to)
      if (c) return c
    }
    path.pop()
    state.set(id, 2)
    return null
  }
  for (const s of def.steps) {
    const c = visit(s.id)
    if (c) return c
  }
  return null
}

// Where a run starts: the trigger's start, else the first step.
export function startOf(def: FlowDefinition, triggerId?: string | null): string {
  const t = triggerId ? def.triggers.find((x) => x.id === triggerId) : def.triggers[0]
  return t?.start ?? def.steps[0]!.id
}
