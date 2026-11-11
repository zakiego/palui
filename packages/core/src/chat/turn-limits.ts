// A turn has two limits: maxSteps and the wall-clock turnTimeoutMs. Hitting
// either while the model is still calling tools used to end the turn with no
// reply. Two guards: the step that probably can't fit is told to answer with
// no tools, and a turn that times out anyway still tells the owner.

// Added after the tool results of the wrap-up step; it never reaches the history.
export const WRAP_UP_NOTE =
  "[System] This turn is almost out of time or steps. Don't call any tool. Reply to the owner now: what you did, what isn't done yet, and what you'd do next. If something is still unfinished, say so plainly and offer to continue."

export type StepClock = { startedAt: number; stepStartedAt: number; longestStepMs: number }

export function stepClock(now: number): StepClock {
  return { startedAt: now, stepStartedAt: now, longestStepMs: 0 }
}

export type WrapUpReason = "steps" | "time"

// Called at the start of each step. Non-null when this step should answer without tools.
export function shouldWrapUp(c: StepClock, o: { stepNumber: number; maxSteps: number; timeoutMs: number; now: number }): WrapUpReason | null {
  if (o.stepNumber > 0) c.longestStepMs = Math.max(c.longestStepMs, o.now - c.stepStartedAt)
  c.stepStartedAt = o.now
  // The first step always gets its tools; a one-step budget has nothing to wrap up.
  if (o.stepNumber === 0) return null
  if (o.stepNumber >= o.maxSteps - 1) return "steps"
  // The next step may take as long as the slowest one so far. If that would run
  // past 80% of the budget, answer now while there's still time to.
  const reserve = Math.max(c.longestStepMs, o.timeoutMs * 0.1)
  return o.now - c.startedAt + reserve > o.timeoutMs * 0.8 ? "time" : null
}

export type RanTool = { name: string; write: boolean; ok: boolean }

export function formatLimit(ms: number): string {
  return ms >= 60_000 && ms % 60_000 === 0 ? `${ms / 60_000} menit` : `${Math.round(ms / 1000)} detik`
}

function counted(names: string[]): string {
  const n = new Map<string, number>()
  for (const x of names) n.set(x, (n.get(x) ?? 0) + 1)
  const parts = [...n].map(([k, v]) => (v > 1 ? `${k} (${v}x)` : k))
  return parts.length > 6 ? `${parts.slice(0, 6).join(", ")}, +${parts.length - 6} lagi` : parts.join(", ")
}

// What the owner sees when a turn hits a limit with no reply: it timed out, or
// the model called a tool on the step told to answer. It is saved as the reply,
// so the next turn knows why it stopped instead of guessing.
export function limitNotice(limit: { timeoutMs: number } | { maxSteps: number }, ran: RanTool[]): string {
  const what = "timeoutMs" in limit ? `udah lewat batas ${formatLimit(limit.timeoutMs)}` : `udah kena batas ${limit.maxSteps} langkah`
  const lines = [`⏱️ Giliranku kepotong: ${what} sebelum aku sempat balas.`]
  if (ran.length) lines.push(`Yang sempat jalan: ${counted(ran.map((r) => r.name))}.`)
  const changed = ran.filter((r) => r.write && r.ok).map((r) => r.name)
  lines.push(changed.length ? `Yang udah ngubah sesuatu: ${counted(changed)}.` : "Belum ada yang diubah.")
  lines.push('Bilang "lanjut" kalau mau aku terusin.')
  return lines.join("\n")
}
