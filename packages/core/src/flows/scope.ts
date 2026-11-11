// What templates and expressions see during a run (RFC flows 4.2).
import type { FlowRow, RunRow } from "./service"
import { nowScope, type Scope } from "./values"

export type RunVars = { steps: Record<string, unknown>; vars: Record<string, unknown>; item?: unknown; index?: number }

export type ScopeInput = {
  run: Pick<RunRow, "id" | "trigger" | "wait">
  flow: Pick<FlowRow, "id" | "name" | "tz" | "state">
  vars: RunVars
  now: number
}

export function scopeOf(c: ScopeInput, extra: Partial<Scope> = {}): Scope {
  const w = (c.run.wait ?? {}) as { followUps?: number }
  return {
    trigger: c.run.trigger ?? {},
    steps: c.vars.steps ?? {},
    vars: c.vars.vars ?? {},
    flow: { ...(c.flow.state?.values ?? {}), name: c.flow.name, id: c.flow.id },
    item: c.vars.item ?? null,
    index: c.vars.index ?? null,
    now: nowScope(c.now, c.flow.tz),
    run: { id: c.run.id, attempt: 1, followUps: w.followUps ?? 0 },
    ...extra,
  }
}
