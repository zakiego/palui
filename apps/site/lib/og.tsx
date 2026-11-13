import { readFile } from "node:fs/promises"
import { join } from "node:path"
import { ImageResponse } from "next/og"
import { Children, cloneElement, Fragment, isValidElement, type ReactElement, type ReactNode } from "react"
import { MASCOT_VIEWBOX, MascotScene } from "@/components/landing/mascot-scene"
import { MARK_CAP, MARK_EYES, MARK_SMILE, MARK_TILE } from "@/components/palui-mark"

// Social cards, 1200 x 630, in the landing's look (Linear's marketing dark,
// the dashboard's tokens): the page black, Inter, the mark and name on top,
// the headline and one muted line at the bottom left. The home card also
// carries the landing's mascot on its stage. Satori can't read CSS variables,
// so the values below are copied from components/landing/landing.css.
const BG = "#08090a"
// The stage: the landing's faint white wash and 8% white hairline, flattened
// over the page black.
const STAGE = "#0c0d0e"
const HAIRLINE = "#1d1e20"
const TEXT = "#f7f8f8"
const DIM = "#8a8f98"
const INK = "#08090a"

export const ogSize = { width: 1200, height: 630 }

// Satori only takes plain elements inside an <svg>, so components and
// fragments are called and unwrapped first. They must not use hooks.
function plain(node: ReactNode): ReactNode {
  if (Array.isArray(node)) return node.map(plain)
  if (!isValidElement(node)) return node
  const el = node as ReactElement<{ children?: ReactNode }>
  if (el.type === Fragment) return Children.toArray(el.props.children).map(plain)
  if (typeof el.type === "function") return plain((el.type as (props: unknown) => ReactNode)(el.props))
  return cloneElement(el, undefined, ...Children.toArray(el.props.children).map(plain))
}

// Satori reads woff but not woff2, and @fontsource ships both. Inter's static
// cuts stop at 500 and 600, so 500 stands in for the landing's 510.
async function font(pkg: string, file: string) {
  return readFile(join(process.cwd(), "node_modules/@fontsource", pkg, "files", file))
}

export async function ogImage({ title, description, kicker, mascot = false }: { title: string; description: string; kicker: string; mascot?: boolean }) {
  const [regular, medium, semibold] = await Promise.all([
    font("inter", "inter-latin-400-normal.woff"),
    font("inter", "inter-latin-500-normal.woff"),
    font("inter", "inter-latin-600-normal.woff"),
  ])
  const lines = title.split("\n")
  const long = lines.length === 1 && title.length > 32
  return new ImageResponse(
    <div style={{ display: "flex", width: "100%", height: "100%", padding: 64, gap: 48, background: BG, color: TEXT, fontFamily: "Inter" }}>
      <div style={{ display: "flex", flexDirection: "column", flex: 1 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
          <svg width="48" height="48" viewBox="0 0 32 32">
            <path d={MARK_TILE} fill={TEXT} />
            <path d={MARK_CAP} fill={INK} />
            {MARK_EYES.map((e) => (
              <circle key={e.cx} cx={e.cx} cy={e.cy} r={e.r} fill={INK} />
            ))}
            <path d={MARK_SMILE} fill={INK} />
          </svg>
          <div style={{ display: "flex", fontSize: 32, fontWeight: 600, letterSpacing: "-0.011em" }}>Palui Agent</div>
          <div style={{ display: "flex", fontSize: 28, color: DIM, marginLeft: 4 }}>{kicker}</div>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 24, marginTop: "auto" }}>
          {/* A "\n" in the title is a forced line break, so the headline
              breaks where it reads best. */}
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              fontSize: long ? 60 : 72,
              fontWeight: 500,
              letterSpacing: "-0.022em",
              lineHeight: 1.05,
            }}
          >
            {lines.map((line) => (
              <div key={line} style={{ display: "flex" }}>
                {line}
              </div>
            ))}
          </div>
          <div style={{ display: "flex", fontSize: 32, color: DIM, lineHeight: 1.4, maxWidth: mascot ? 620 : 980 }}>{description}</div>
          <div style={{ display: "flex", marginTop: 8, fontSize: 24, color: DIM }}>palui.zakiego.com</div>
        </div>
      </div>
      {mascot ? (
        <div style={{ display: "flex", alignItems: "center", justifyContent: "center", width: 400, border: `2px solid ${HAIRLINE}`, borderRadius: 16, background: STAGE }}>
          <svg width="330" height="338" viewBox={MASCOT_VIEWBOX}>
            {plain(<MascotScene />)}
          </svg>
        </div>
      ) : null}
    </div>,
    {
      ...ogSize,
      fonts: [
        { name: "Inter", data: regular, weight: 400, style: "normal" },
        { name: "Inter", data: medium, weight: 500, style: "normal" },
        { name: "Inter", data: semibold, weight: 600, style: "normal" },
      ],
    },
  )
}
