// The landing hero's script. Palui stands behind the three things it keeps for
// you, and asks type themselves into the box above it, one per thing. Once a
// line is written Palui turns to the matching thing and that thing does its
// bit (a note gets written, the alarm rings, a hammer comes out of the
// toolbox), then Palui answers. The script is plain data, so the component
// only has to wait `beat.wait` and move to the next beat.

/**
 * The things on the slab, left to right, each with the ask that sets it off.
 * `gaze` is where Palui's eyes go to look at one (-1 to 1 across and down its face).
 */
export const KEEPS = [
  { id: "notes", gaze: [-1, 1], ask: "My passport expires in March", reply: "Noted, I’ll remember that" },
  { id: "clock", gaze: [0, 1], ask: "Remind me at 9 to pay the bill", reply: "Okay, I’ll ping you at 9" },
  { id: "toolbox", gaze: [1, 1], ask: "Move my dentist to Friday", reply: "Done, it’s on Friday at 10" },
] as const

export type KeepId = (typeof KEEPS)[number]["id"]

/** Typing is slow enough to read along: one character at a time, after a short pause. */
export const TYPE_MS = 55
export const TYPE_LEAD = 500
/** How long `text` takes to type, with the pause before the first key. */
export const typeTime = (text: string) => TYPE_LEAD + text.length * TYPE_MS

export const TIMING = {
  /** The pause between the last key and the thing acting. */
  settle: 350,
  /** How long one thing has the stage. The longest act (the alarm) takes 1400. */
  act: 2400,
  /** How long the answer stays up. */
  reply: 1900,
  /** The box and the answer clear before the next ask. */
  clear: 320,
}

export type StagePhase = "type" | "act" | "reply" | "clear"

export type StageBeat = {
  /** Index into KEEPS of the ask on stage. */
  keep: number
  phase: StagePhase
  wait: number
}

/** One round of the loop: every ask typed, acted out, answered and cleared, in order. */
export const STAGE_BEATS: StageBeat[] = KEEPS.flatMap((k, keep) => [
  { keep, phase: "type" as const, wait: typeTime(k.ask) + TIMING.settle },
  { keep, phase: "act" as const, wait: TIMING.act },
  { keep, phase: "reply" as const, wait: TIMING.reply },
  { keep, phase: "clear" as const, wait: TIMING.clear },
])
