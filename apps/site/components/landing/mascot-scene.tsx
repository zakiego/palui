import type { ReactNode } from "react"
import { MARK_EYES, MARK_SMILE } from "@/components/palui-mark"
import type { KeepId } from "@/lib/mascot"
import { Box, Floor, face, onLeftFront, onTop, type Point, screen, TONES } from "./iso"

// The landing mascot, as plain SVG with no hooks, so the page and the social
// card (lib/og.tsx) draw the same picture. Palui is a block figure: the mark's
// tile extruded into a head, with the mark's own eyes and smile on its left
// front and the kopiah as a real box on top. In front of it stand the three
// things it keeps for you (lib/mascot.ts): a stack of notes, an alarm clock
// and a toolbox. It's an original drawing, not the comic's art.
//
// With `alive`, each thing also carries the parts it needs to act (a fresh
// sheet, ring marks, a hammer). landing.css hides them until their turn, so
// the still picture is the same either way. The social card leaves `alive`
// off, because it has no stylesheet to hide them.

export const MASCOT_VIEWBOX = "-166 -145 346 354"
export const MASCOT_ALT = "Palui, a blocky figure in a kopiah cap, standing behind a stack of notes, an alarm clock and a toolbox."

const HEAD: { at: Point; size: Point } = { at: [2.6, 1.2, 3.7], size: [5, 4, 4.2] }
// The mark is 32 wide and so is the head, so one mark unit is this many units.
const MARK = HEAD.size[0] / 32

function Palui({ look }: { look?: KeepId }) {
  return (
    <>
      <path d={face([3.5, 2.2, 0], [8, 2.2, 0], [8, 5, 0], [3.5, 5, 0])} fill="#0c0c0c" />
      <Box at={[2.5, 2.8, 1.6]} size={[0.8, 0.8, 1.8]} tone={TONES.body} />
      <Box at={[3.7, 2.6, 0]} size={[1.2, 1.2, 1]} tone={TONES.leg} />
      <Box at={[5.3, 2.6, 0]} size={[1.2, 1.2, 1]} tone={TONES.leg} />
      <Box at={[3.3, 2, 1]} size={[3.6, 2.4, 2.6]} tone={TONES.body} />
      <Box at={[6.9, 2.8, 1.6]} size={[0.8, 0.8, 1.8]} tone={TONES.body} />
      {/* The head leans toward the thing Palui looks at. */}
      <g className="lp-head" data-look={look}>
        <Box at={HEAD.at} size={HEAD.size} tone={TONES.head} />
        {/* The face is larger and sits lower than in the mark, where the cap
            shares the tile with it. `data-gaze` is how far a part slides when
            Palui looks around (use-gaze.ts), in mark units. */}
        <g transform={`${onLeftFront([HEAD.at[0], HEAD.at[1] + HEAD.size[1], HEAD.at[2] + HEAD.size[2]], MARK)} translate(16 15.5) scale(1.45) translate(-16 -21.2)`} fill="#0a0a0a">
          <g transform="translate(0 -1.4)">
            <g data-gaze="1.5 1.1">
              {MARK_EYES.map((eye) => (
                <circle key={eye.cx} className="lp-eye" cx={eye.cx} cy={eye.cy} r={1.7} />
              ))}
            </g>
          </g>
          <path data-gaze="0.6 0.45" d={MARK_SMILE} />
        </g>
        <Box at={[3, 1.6, 7.9]} size={[4.2, 3.2, 1.6]} tone={TONES.cap} taper={0.35} />
      </g>
    </>
  )
}

// Four sheets, a little out of line, with two written lines on the top one.
const SHEETS: [number, number][] = [
  [0, 0],
  [0.12, -0.06],
  [-0.06, 0.1],
  [0.08, 0.02],
]
const TOP_SHEET = SHEETS[SHEETS.length - 1]
const SHEET_THICK = 0.2
// The written lines: where each starts on a sheet, and how long it is.
const WRITING: { at: [number, number]; length: number }[] = [
  { at: [1.35, 6.75], length: 1.3 },
  { at: [1.35, 7.15], length: 0.9 },
]

// When Palui remembers, a fresh sheet drops onto the stack and its two lines
// get written. It sits exactly one sheet above the old top one, so when it
// sinks back and fades the stack reads as settling, not as a sheet vanishing.
function Notes({ alive }: { alive: boolean }) {
  const top = SHEETS.length * SHEET_THICK
  return (
    <>
      {SHEETS.map(([dx, dy], i) => (
        <Box key={`${dx}:${dy}`} at={[0.9 + dx, 6.3 + dy, i * SHEET_THICK]} size={[2, 1.6, SHEET_THICK]} tone={TONES.paper} />
      ))}
      {WRITING.map(({ at: [x, y], length }) => (
        <path key={y} d={face([x, y, top], [x + length, y, top], [x + length, y + 0.14, top], [x, y + 0.14, top])} fill="#8a8a8a" />
      ))}
      {alive ? (
        <g className="lp-note-new">
          <Box at={[0.9 + TOP_SHEET[0], 6.3 + TOP_SHEET[1], top]} size={[2, 1.6, SHEET_THICK]} tone={TONES.paper} />
          {WRITING.map(({ at: [x, y], length }, i) => (
            <g key={y} transform={onTop([x, y, top + SHEET_THICK])}>
              <rect className="lp-note-line" data-line={i + 1} width={length} height="0.14" fill="#8a8a8a" />
            </g>
          ))}
        </g>
      ) : null}
    </>
  )
}

// The marks an alarm throws off, fanned out above it on the dark slab and
// drawn straight on the screen.
const RING_MARKS = ["M-56 66l-5 -7", "M-41.6 59v-8", "M-28 68l4 -7"]

// Two feet, a body with a dial on its left front, a button on top. When Palui
// reminds, the clock rattles on its feet and the marks flash above it.
function Clock({ alive }: { alive: boolean }) {
  return (
    <>
      <g className="lp-clock">
        <Box at={[4.2, 6.7, 0]} size={[0.35, 0.7, 0.25]} tone={TONES.leg} />
        <Box at={[5.35, 6.7, 0]} size={[0.35, 0.7, 0.25]} tone={TONES.leg} />
        <Box at={[4.1, 6.5, 0.25]} size={[1.7, 1.1, 1.7]} tone={TONES.clock} />
        <g transform={onLeftFront([4.1, 7.6, 1.95])}>
          <circle cx="0.85" cy="0.85" r="0.62" fill="#f5f5f5" />
          <path d="M0.85 0.85V0.45M0.85 0.85H1.14" fill="none" stroke="#0a0a0a" strokeWidth="0.12" strokeLinecap="round" />
        </g>
        <Box at={[4.7, 6.8, 1.95]} size={[0.5, 0.5, 0.22]} tone={TONES.cap} />
      </g>
      {alive ? (
        <g className="lp-ring" fill="none" stroke="#ededed" strokeWidth="2" strokeLinecap="round">
          {RING_MARKS.map((d) => (
            <path key={d} d={d} />
          ))}
        </g>
      ) : null}
    </>
  )
}

const BOX: { at: Point; size: Point } = { at: [7.1, 6.4, 0], size: [2.3, 1.4, 1] }
/** Where the lid meets the box. */
const RIM = 0.7
const [BX0, BY0] = BOX.at
const [BX1, BY1] = [BX0 + BOX.size[0], BY0 + BOX.size[1]]
// Everything above the two front edges of the open box. The hammer is drawn
// inside it, so the box's own front hides whatever is still down in the box.
const ABOVE_RIM = (() => {
  const [left, front, right] = [screen([BX0, BY1, RIM]), screen([BX1, BY1, RIM]), screen([BX1, BY0, RIM])]
  const sky = -400
  return `M${[left, front, right, [right[0], sky], [left[0], sky]].map((p) => p.map((n) => n.toFixed(1)).join(",")).join("L")}Z`
})()

// A box and a lid with a seam between them, a latch across the seam and a
// handle on the lid. When Palui uses a tool, the lid slides back and a hammer
// comes up out of the box and taps twice.
function Toolbox({ alive }: { alive: boolean }) {
  return (
    <>
      <Box at={BOX.at} size={[BOX.size[0], BOX.size[1], RIM]} tone={TONES.toolboxOpen} />
      <path d={face([8.05, BY1, 0.5], [8.45, BY1, 0.5], [8.45, BY1, RIM], [8.05, BY1, RIM])} fill="#ededed" />
      <g className="lp-lid">
        <Box at={[BX0, BY0, RIM]} size={[BOX.size[0], BOX.size[1], BOX.size[2] - RIM]} tone={TONES.toolbox} />
        <path d={face([BX0, BY1, 0.66], [BX1, BY1, 0.66], [BX1, BY1, 0.74], [BX0, BY1, 0.74])} fill="#2c2c2c" />
        <path d={face([BX1, BY0, 0.66], [BX1, BY1, 0.66], [BX1, BY1, 0.74], [BX1, BY0, 0.74])} fill="#202020" />
        <path d={face([8.05, BY1, 0.66], [8.45, BY1, 0.66], [8.45, BY1, 0.86], [8.05, BY1, 0.86])} fill="#ededed" />
        <Box at={[7.85, 6.95, 1]} size={[0.8, 0.3, 0.3]} tone={TONES.cap} />
      </g>
      {alive ? (
        <>
          <clipPath id="lp-above-rim">
            <path d={ABOVE_RIM} />
          </clipPath>
          <g clipPath="url(#lp-above-rim)">
            <g className="lp-hammer">
              <g className="lp-hammer-swing">
                <Box at={[8.12, 7.2, 0.3]} size={[0.26, 0.26, 1.9]} tone={TONES.clock} />
                <Box at={[7.75, 7.1, 2.2]} size={[1, 0.46, 0.46]} tone={TONES.paper} />
              </g>
            </g>
          </g>
        </>
      ) : null}
    </>
  )
}

const THINGS: { id: KeepId; Thing: (props: { alive: boolean }) => ReactNode }[] = [
  { id: "notes", Thing: Notes },
  { id: "clock", Thing: Clock },
  { id: "toolbox", Thing: Toolbox },
]

/**
 * Everything inside the mascot's `<svg>`. `acting` is the index of the thing
 * Palui is looking at, which is the one doing its bit. `alive` adds the parts
 * the things act with, and needs landing.css on the page.
 */
export function MascotScene({ acting = null, alive = false }: { acting?: number | null; alive?: boolean }) {
  return (
    <>
      <g className="lp-stage">
        <Floor at={[0, 0]} size={[10, 9.2]} />
        <Palui look={acting === null ? undefined : THINGS[acting].id} />
      </g>
      {THINGS.map(({ id, Thing }, i) => (
        <g key={id} className="lp-keep" data-keep={id} data-acting={acting === i ? "" : undefined}>
          <Thing alive={alive} />
        </g>
      ))}
    </>
  )
}
