// The isometric drawing kit the mascot is built from: a point
// in units goes to a point on the screen, and every solid is a box of three
// flat grays (top, left front, right front), so depth comes from tone alone.

export const COS = Math.cos(Math.PI / 6)
export const UNIT = 20

export type Point = [number, number, number]
export type Tone = { top: string; left: string; right: string }

export const screen = ([x, y, z]: Point): [number, number] => [(x - y) * COS * UNIT, (x + y) * 0.5 * UNIT - z * UNIT]

/** A closed path through the given points. */
export const face = (...points: Point[]) => `M${points.map((p) => screen(p).map((n) => n.toFixed(1)).join(",")).join("L")}Z`

/** A transform that lays flat artwork (in units) on a left-front plane, with its top left corner at `corner`. */
export function onLeftFront(corner: Point, scale = 1) {
  const [x, y] = screen(corner)
  const k = UNIT * scale
  return `matrix(${(k * COS).toFixed(4)} ${(k * 0.5).toFixed(4)} 0 ${k.toFixed(4)} ${x.toFixed(1)} ${y.toFixed(1)})`
}

/** The same for a top plane: local x runs along the scene's x, local y along its y. */
export function onTop(corner: Point, scale = 1) {
  const [x, y] = screen(corner)
  const k = UNIT * scale
  return `matrix(${(k * COS).toFixed(4)} ${(k * 0.5).toFixed(4)} ${(-k * COS).toFixed(4)} ${(k * 0.5).toFixed(4)} ${x.toFixed(1)} ${y.toFixed(1)})`
}

export const TONES = {
  floor: { top: "#151515", left: "#101010", right: "#0c0c0c" },
  head: { top: "#ffffff", left: "#e6e6e6", right: "#b0b0b0" },
  cap: { top: "#5e5e5e", left: "#3d3d3d", right: "#242424" },
  body: { top: "#808080", left: "#5e5e5e", right: "#3e3e3e" },
  leg: { top: "#4a4a4a", left: "#3a3a3a", right: "#282828" },
  paper: { top: "#ffffff", left: "#d8d8d8", right: "#a6a6a6" },
  clock: { top: "#d2d2d2", left: "#b2b2b2", right: "#7c7c7c" },
  toolbox: { top: "#6e6e6e", left: "#525252", right: "#363636" },
  /** The toolbox below its lid: the same sides, and a dark top for the inside. */
  toolboxOpen: { top: "#0e0e0e", left: "#525252", right: "#363636" },
} satisfies Record<string, Tone>

// A box seen from the front right. `taper` pulls the top in on every side.
// The stroke has the fill's color and round joins, which rounds the corners.
export function Box({ at, size, tone, taper = 0, edge }: { at: Point; size: Point; tone: Tone; taper?: number; edge?: string }) {
  const [x0, y0, z0] = at
  const [x1, y1, z1] = [x0 + size[0], y0 + size[1], z0 + size[2]]
  const t = taper
  const sides: [string, string][] = [
    [face([x1, y0, z0], [x1, y1, z0], [x1 - t, y1 - t, z1], [x1 - t, y0 + t, z1]), tone.right],
    [face([x0, y1, z0], [x1, y1, z0], [x1 - t, y1 - t, z1], [x0 + t, y1 - t, z1]), tone.left],
    [face([x0 + t, y0 + t, z1], [x1 - t, y0 + t, z1], [x1 - t, y1 - t, z1], [x0 + t, y1 - t, z1]), tone.top],
  ]
  return (
    <g strokeWidth={edge ? 1 : 2.4} strokeLinejoin="round">
      {sides.map(([d, fill]) => (
        <path key={d} d={d} fill={fill} stroke={edge ?? fill} />
      ))}
    </g>
  )
}

/** The slab Palui stands on. */
export function Floor({ at, size }: { at: [number, number]; size: [number, number] }) {
  return <Box at={[at[0], at[1], -0.5]} size={[size[0], size[1], 0.5]} tone={TONES.floor} edge="#272727" />
}
