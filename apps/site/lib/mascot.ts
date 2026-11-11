// The landing mascot's loop. Palui stands behind the three things it keeps for
// you. Every few seconds it turns to one of them and that thing does its bit:
// a note gets written, the alarm rings, a hammer comes out of the toolbox. The
// picture has no labels, so the acting is what says what each thing is for.
// The clock is a pure function, so the component only has to wait `beat.wait`
// and move to the next beat.

/** The things on the slab, left to right. `gaze` is where Palui's eyes go to look at one (-1 to 1 across and down its face). */
export const KEEPS = [
  { id: "notes", gaze: [-1, 1] },
  { id: "clock", gaze: [0, 1] },
  { id: "toolbox", gaze: [1, 1] },
] as const

export type KeepId = (typeof KEEPS)[number]["id"]

export const TIMING = {
  /** The entrance finishes before the first act. */
  first: 1400,
  /** How long one thing has the stage. The longest act (the alarm) takes 1400. */
  look: 2400,
  /** The quiet time between two acts. */
  rest: 1200,
}

export type Beat = {
  /** Index into KEEPS of the thing that is acting, or null while everything rests. */
  focus: number | null
  wait: number
}

/** Beat `n` of the loop: a rest, an act, a rest, the next act, and so on forever. */
export function beatAt(n: number): Beat {
  if (n % 2 === 0) return { focus: null, wait: n === 0 ? TIMING.first : TIMING.rest }
  return { focus: ((n - 1) / 2) % KEEPS.length, wait: TIMING.look }
}
