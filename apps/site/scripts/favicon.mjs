// Builds the favicon set from the landing mascot's head: the head box, the
// mark's eyes and smile on its left front, and the kopiah on top, on an ink
// tile. The geometry is copied from components/landing/iso.tsx and
// mascot-scene.tsx, with the face transforms flattened into one matrix each.
//
//   node apps/site/scripts/favicon.mjs
//
// It writes app/icon.svg and the app's favicon.svg, then rasterizes
// favicon.ico and apple-icon.png. Rasterizing goes through macOS Quick Look
// (WebKit), because ImageMagick's built-in SVG renderer drops the face.
import { execFileSync } from "node:child_process"
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs"
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
