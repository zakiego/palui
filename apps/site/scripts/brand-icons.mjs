// Builds the favicon set and the /brand downloads. The favicons are the
// landing mascot's head: the head box, the mark's eyes and smile on its left
// front, and the kopiah on top, on an ink tile. The geometry is copied from
// components/landing/iso.tsx and mascot-scene.tsx, with the face transforms
// flattened into one matrix each. The mark's paths are copied from
// components/palui-mark.tsx.
//
//   node apps/site/scripts/brand-icons.mjs
//
// It writes app/icon.svg and the app's favicon.svg, then rasterizes
// favicon.ico and apple-icon.png. It also writes the profile pictures and the
// mark to public/brand (SVG, plus a 1024 px PNG for each profile picture).
// Rasterizing goes through macOS Quick Look (WebKit), because ImageMagick's
// built-in SVG renderer drops the face.
import { execFileSync } from "node:child_process"
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

const COS = Math.cos(Math.PI / 6)
const UNIT = 20
const screen = ([x, y, z]) => [(x - y) * COS * UNIT, (x + y) * 0.5 * UNIT - z * UNIT]
const face = (...pts) => `M${pts.map((p) => screen(p).map((n) => n.toFixed(1)).join(",")).join("L")}Z`
const bounds = []
function box(at, size, tone, taper = 0) {
  const [x0, y0, z0] = at, [x1, y1, z1] = [x0 + size[0], y0 + size[1], z0 + size[2]], t = taper
  const sides = [
    [[[x1, y0, z0], [x1, y1, z0], [x1 - t, y1 - t, z1], [x1 - t, y0 + t, z1]], tone.right],
    [[[x0, y1, z0], [x1, y1, z0], [x1 - t, y1 - t, z1], [x0 + t, y1 - t, z1]], tone.left],
    [[[x0 + t, y0 + t, z1], [x1 - t, y0 + t, z1], [x1 - t, y1 - t, z1], [x0 + t, y1 - t, z1]], tone.top],
  ]
  for (const [pts] of sides) for (const p of pts) bounds.push(screen(p))
  return `<g stroke-width="2.4" stroke-linejoin="round">${sides.map(([pts, f]) => `<path d="${face(...pts)}" fill="${f}" stroke="${f}"/>`).join("")}</g>`
}
const HEAD = { at: [2.6, 1.2, 3.7], size: [5, 4, 4.2] }
const MARK = HEAD.size[0] / 32
const head = { top: "#ffffff", left: "#e6e6e6", right: "#b0b0b0" }
const cap = { top: "#5e5e5e", left: "#3d3d3d", right: "#242424" }
const SMILE = "M10.62 21.43a1.3 1.3 0 0 1 1.81-.28Q16 23.8 19.57 21.15a1.3 1.3 0 1 1 1.53 2.1Q16 27 10.9 23.25a1.3 1.3 0 0 1-.28-1.82Z"
// 2D affine matrices as [a b c d e f], the same order as SVG matrix().
const mul = (A, B) => [
  A[0] * B[0] + A[2] * B[1],
  A[1] * B[0] + A[3] * B[1],
  A[0] * B[2] + A[2] * B[3],
  A[1] * B[2] + A[3] * B[3],
  A[0] * B[4] + A[2] * B[5] + A[4],
  A[1] * B[4] + A[3] * B[5] + A[5],
]
const T = (x, y) => [1, 0, 0, 1, x, y], Sc = (k) => [k, 0, 0, k, 0, 0]
const lf = (() => { const [x, y] = screen([HEAD.at[0], HEAD.at[1] + HEAD.size[1], HEAD.at[2] + HEAD.size[2]]), k = UNIT * MARK; return [k * COS, k * 0.5, 0, k, x, y] })()
const faceM = [T(16, 15.5), Sc(1.45), T(-16, -21.2)].reduce(mul, lf)
const eyeM = mul(faceM, T(0, -1.4))
const mat = (m) => `matrix(${m.map((n) => +n.toFixed(4)).join(" ")})`
const art =
  box(HEAD.at, HEAD.size, head) +
  `<g fill="#0a0a0a"><g transform="${mat(eyeM)}"><circle cx="12.4" cy="18.2" r="1.7"/><circle cx="19.6" cy="18.2" r="1.7"/></g><path transform="${mat(faceM)}" d="${SMILE}"/></g>` +
  box([3, 1.6, 7.9], [4.2, 3.2, 1.6], cap, 0.35)

const xs = bounds.map((b) => b[0])
const ys = bounds.map((b) => b[1])
const [minX, maxX, minY, maxY] = [Math.min(...xs) - 1.2, Math.max(...xs) + 1.2, Math.min(...ys) - 1.2, Math.max(...ys) + 1.2]
const cx = (minX + maxX) / 2
const cy = (minY + maxY) / 2
const side = Math.max(maxX - minX, maxY - minY)

/** The head on an ink tile. `pad` is the margin as a share of the head's size; `radius` 0 gives a square for iOS to round itself. */
function tile(pad, radius) {
  const S = side * (1 + pad * 2)
  const [x, y] = [cx - S / 2, cy - S / 2].map((n) => n.toFixed(1))
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${x} ${y} ${S.toFixed(1)} ${S.toFixed(1)}"><rect x="${x}" y="${y}" width="${S.toFixed(1)}" height="${S.toFixed(1)}" rx="${(S * radius).toFixed(1)}" fill="#0a0a0a"/>${art}</svg>\n`
}

const root = new URL("..", import.meta.url).pathname
const icon = tile(0.1, 0.22)
writeFileSync(join(root, "app/icon.svg"), icon)
writeFileSync(join(root, "../app/public/app/favicon.svg"), icon)

const tmp = mkdtempSync(join(tmpdir(), "palui-favicon-"))
writeFileSync(join(tmp, "icon.svg"), icon)
writeFileSync(join(tmp, "apple.svg"), tile(0.16, 0))
execFileSync("qlmanage", ["-t", "-s", "1024", "-o", tmp, join(tmp, "icon.svg"), join(tmp, "apple.svg")], { stdio: "ignore" })
execFileSync("magick", [join(tmp, "icon.svg.png"), "-filter", "Lanczos", "-define", "icon:auto-resize=48,32,16", join(root, "app/favicon.ico")])
execFileSync("magick", [join(tmp, "apple.svg.png"), "-filter", "Lanczos", "-resize", "180x180", "-strip", join(root, "app/apple-icon.png")])
console.log(`wrote icon.svg, favicon.svg, favicon.ico and apple-icon.png (${readFileSync(join(root, "app/icon.svg")).length} byte svg)`)

// ------------------------------------------------------------------ brand
// Profile pictures are full-bleed squares, because WhatsApp and Telegram crop
// them to a circle. Everything that matters sits inside that circle. Each
// comes on dark and on light; only the background changes, the character
// keeps its own grays.
const TILE = "M0 8a8 8 0 0 1 8-8h16a8 8 0 0 1 8 8v16a8 8 0 0 1-8 8H8a8 8 0 0 1-8-8Z"
const CAP = "M10.2 6h11.6a1.2 1.2 0 0 1 1.17.93l1.5 6.6a1.2 1.2 0 0 1-1.17 1.47H8.7a1.2 1.2 0 0 1-1.17-1.47l1.5-6.6A1.2 1.2 0 0 1 10.2 6Z"
const markFace = (ink) =>
  `<g fill="${ink}"><path d="${CAP}"/><circle cx="12.4" cy="18.2" r="1.5"/><circle cx="19.6" cy="18.2" r="1.5"/><path d="${SMILE}"/></g>`

// The light background is a mid-light gray, not white, so the head's white
// top and light left side still read against it.
const THEMES = {
  dark: { bg: "#0a0a0a", shadow: "#1a1a1a" },
  light: { bg: "#c8c8c8", shadow: "#adadad" },
}

// The body under the head, copied from Palui() in mascot-scene.tsx: the
// shadow, the back arm, two legs, the torso and the front arm, in that order.
bounds.length = 0
const body = { top: "#808080", left: "#5e5e5e", right: "#3e3e3e" }
const leg = { top: "#4a4a4a", left: "#3a3a3a", right: "#282828" }
const SHADOW = [[3.5, 2.2, 0], [8, 2.2, 0], [8, 5, 0], [3.5, 5, 0]]
const bodyArt =
  box([2.5, 2.8, 1.6], [0.8, 0.8, 1.8], body) +
  box([3.7, 2.6, 0], [1.2, 1.2, 1], leg) +
  box([5.3, 2.6, 0], [1.2, 1.2, 1], leg) +
  box([3.3, 2, 1], [3.6, 2.4, 2.6], body) +
  box([6.9, 2.8, 1.6], [0.8, 0.8, 1.8], body)
const bodyBounds = [...bounds, ...SHADOW.map(screen)]
const fullXs = [...bodyBounds.map((b) => b[0]), minX, maxX]
const fullYs = [...bodyBounds.map((b) => b[1]), minY, maxY]
const full = { x0: Math.min(...fullXs), x1: Math.max(...fullXs), y0: Math.min(...fullYs), y1: Math.max(...fullYs) }

// The head seen straight on instead of from the corner: one-point
// perspective with the vanishing point high above the middle, so the front
// is flat on and the tops of the head and the kopiah show. Points are
// [x, z, depth] in the same units, x centered on the head.
const VP = 14
const toward = (d) => 1 / (1 + d * 0.07)
const front = ([x, z, d]) => [x * toward(d) * UNIT, -(VP + (z - VP) * toward(d)) * UNIT]
const frontPath = (...pts) => `M${pts.map((p) => front(p).map((n) => n.toFixed(1)).join(",")).join("L")}Z`
const frontPts = []
const poly = (fill, ...pts) => {
  frontPts.push(...pts.map(front))
  return `<path d="${frontPath(...pts)}" fill="${fill}" stroke="${fill}" stroke-width="2.4" stroke-linejoin="round"/>`
}
const [HW, HH, HD] = [HEAD.size[0] / 2, HEAD.size[2], HEAD.size[1]]
const [CW, CT, CH, CD0, CD1] = [2.1, 0.35, 1.6, 0.4, 3.6]
const kFront = UNIT * MARK
const faceFront = [T(16, 15.5), Sc(1.45), T(-16, -21.2)].reduce(mul, [kFront, 0, 0, kFront, -HW * UNIT, -HH * UNIT])
const frontArt =
  poly(head.left, [-HW, 0, 0], [HW, 0, 0], [HW, HH, 0], [-HW, HH, 0]) +
  poly(head.top, [-HW, HH, 0], [HW, HH, 0], [HW, HH, HD], [-HW, HH, HD]) +
  `<g fill="#0a0a0a"><g transform="${mat(mul(faceFront, T(0, -1.4)))}"><circle cx="12.4" cy="18.2" r="1.7"/><circle cx="19.6" cy="18.2" r="1.7"/></g><path transform="${mat(faceFront)}" d="${SMILE}"/></g>` +
  poly(cap.right, [-CW, HH, CD0], [-CW, HH, CD1], [-CW + CT, HH + CH, CD1 - CT], [-CW + CT, HH + CH, CD0 + CT]) +
  poly(cap.right, [CW, HH, CD0], [CW, HH, CD1], [CW - CT, HH + CH, CD1 - CT], [CW - CT, HH + CH, CD0 + CT]) +
  poly(cap.top, [-CW + CT, HH + CH, CD0 + CT], [CW - CT, HH + CH, CD0 + CT], [CW - CT, HH + CH, CD1 - CT], [-CW + CT, HH + CH, CD1 - CT]) +
  poly(cap.left, [-CW, HH, CD0], [CW, HH, CD0], [CW - CT, HH + CH, CD0 + CT], [-CW + CT, HH + CH, CD0 + CT])
const fb = { x0: Math.min(...frontPts.map((p) => p[0])), x1: Math.max(...frontPts.map((p) => p[0])), y0: Math.min(...frontPts.map((p) => p[1])), y1: Math.max(...frontPts.map((p) => p[1])) }

// Palui waving: the same body with the front arm out at the shoulder and up
// past the head, a hand on top, and two marks beside the hand for the wave.
// The arm stands out past the head's right side (x 7.6), so it's drawn
// after the head.
bounds.length = 0
const waveBody =
  box([2.5, 2.8, 1.6], [0.8, 0.8, 1.8], body) +
  box([3.7, 2.6, 0], [1.2, 1.2, 1], leg) +
  box([5.3, 2.6, 0], [1.2, 1.2, 1], leg) +
  box([3.3, 2, 1], [3.6, 2.4, 2.6], body)
const waveArm =
  box([6.9, 2, 2.6], [2.7, 0.8, 0.8], body) +
  box([8.8, 2, 3.4], [0.8, 0.8, 4], body) +
  box([8.7, 1.9, 7.4], [1, 1, 0.9], head)
const waveXs = [...bounds.map((b) => b[0]), ...SHADOW.map((p) => screen(p)[0]), minX, maxX]
const waveYs = [...bounds.map((b) => b[1]), ...SHADOW.map((p) => screen(p)[1]), minY, maxY]
const [hx, hy] = screen([9.7, 2.4, 8])
const waveMarks = (ink) =>
  `<g fill="none" stroke="${ink}" stroke-width="3" stroke-linecap="round"><path d="M${(hx + 14).toFixed(1)} ${(hy - 22).toFixed(1)}q9 8 7 20"/><path d="M${(hx + 26).toFixed(1)} ${(hy - 30).toFixed(1)}q13 12 10 30"/></g>`
const wb = { x0: Math.min(...waveXs), x1: Math.max(...waveXs) + 40, y0: Math.min(...waveYs) - 10, y1: Math.max(...waveYs) }

/** A square of side `S` centered on (x, y), filled with `bg`, with `content` on it. */
function square(x, y, S, bg, content) {
  const [vx, vy, s] = [x - S / 2, y - S / 2, S].map((n) => n.toFixed(1))
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="${vx} ${vy} ${s} ${s}"><rect x="${vx}" y="${vy}" width="${s}" height="${s}" fill="${bg}"/>${content}</svg>\n`
}

const designs = {
  // The head and its kopiah. 0.2 of padding keeps it inside the circle.
  head: ({ bg }) => square(cx, cy, side * 1.4, bg, art),
  // The head straight on.
  front: ({ bg }) => square((fb.x0 + fb.x1) / 2, (fb.y0 + fb.y1) / 2, Math.max(fb.x1 - fb.x0, fb.y1 - fb.y0) * 1.42, bg, frontArt),
  // A portrait: the head high in the frame, the shoulders and arms cut off by
  // the bottom of the circle, the way a photo crops a person.
  bust: ({ bg }) => {
    const S = side * 1.22
    return square(cx + side * 0.02, minY - S * 0.07 + S / 2, S, bg, bodyArt + art)
  },
  // Palui standing on its shadow.
  body: ({ bg, shadow }) => {
    const S = Math.max(full.x1 - full.x0, full.y1 - full.y0) * 1.18
    return square((full.x0 + full.x1) / 2, (full.y0 + full.y1) / 2, S, bg, `<path d="${face(...SHADOW)}" fill="${shadow}"/>${bodyArt}${art}`)
  },
  // Palui waving hello.
  wave: ({ bg, shadow }) => {
    const S = Math.max(wb.x1 - wb.x0, wb.y1 - wb.y0) * 1.16
    const ink = bg === THEMES.dark.bg ? "#a1a1a1" : "#5e5e5e"
    return square((wb.x0 + wb.x1) / 2, (wb.y0 + wb.y1) / 2, S, bg, `<path d="${face(...SHADOW)}" fill="${shadow}"/>${waveBody}${art}${waveArm}${waveMarks(ink)}`)
  },
  // The mark as an app icon: its rounded tile, smaller, in the middle.
  tile: ({ bg }) => {
    const [fill, ink] = bg === THEMES.dark.bg ? ["#ffffff", "#0a0a0a"] : ["#0a0a0a", "#ffffff"]
    return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-12 -12 56 56"><rect x="-12" y="-12" width="56" height="56" fill="${bg === THEMES.dark.bg ? bg : "#ffffff"}"/><path d="${TILE}" fill="${fill}"/>${markFace(ink)}</svg>\n`
  },
  // The mark's face alone, so the circle crop becomes the tile. The face
  // spans x 7.5 to 24.5 and y 6 to 26.5; 38 wide leaves its farthest corner
  // well inside the circle.
  face: ({ bg }) => {
    const ink = bg === THEMES.dark.bg ? "#ffffff" : "#0a0a0a"
    const paper = bg === THEMES.dark.bg ? bg : "#ffffff"
    return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-3 -2.75 38 38"><rect x="-3" y="-2.75" width="38" height="38" fill="${paper}"/>${markFace(ink)}</svg>\n`
  },
}

const markTile = (tileFill, ink) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><path d="${TILE}" fill="${tileFill}"/>${markFace(ink)}</svg>\n`

const brandDir = join(root, "public/brand")
rmSync(brandDir, { recursive: true, force: true })
mkdirSync(brandDir, { recursive: true })
const avatars = {}
for (const [design, draw] of Object.entries(designs)) {
  for (const [theme, colors] of Object.entries(THEMES)) avatars[`palui-avatar-${design}-${theme}`] = draw(colors)
}
for (const [name, svg] of Object.entries(avatars)) {
  writeFileSync(join(brandDir, `${name}.svg`), svg)
  writeFileSync(join(tmp, `${name}.svg`), svg)
}
writeFileSync(join(brandDir, "palui-mark-on-dark.svg"), markTile("#ffffff", "#0a0a0a"))
writeFileSync(join(brandDir, "palui-mark-on-light.svg"), markTile("#0a0a0a", "#ffffff"))
execFileSync("qlmanage", ["-t", "-s", "1024", "-o", tmp, ...Object.keys(avatars).map((n) => join(tmp, `${n}.svg`))], { stdio: "ignore" })
for (const name of Object.keys(avatars)) {
  execFileSync("magick", [join(tmp, `${name}.svg.png`), "-resize", "1024x1024", "-alpha", "off", "-strip", "-define", "png:color-type=2", join(brandDir, `${name}.png`)])
}
console.log(`wrote ${Object.keys(avatars).length} profile pictures and 2 marks to public/brand`)
