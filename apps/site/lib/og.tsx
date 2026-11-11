import { readFile } from "node:fs/promises"
import { join } from "node:path"
import { ImageResponse } from "next/og"
import { Children, cloneElement, Fragment, isValidElement, type ReactElement, type ReactNode } from "react"
import { MASCOT_VIEWBOX, MascotScene } from "@/components/landing/mascot-scene"
import { MARK_CAP, MARK_EYES, MARK_SMILE, MARK_TILE } from "@/components/palui-mark"

// Social cards, 1200 x 630, in the landing's dark look: a window on
// neutral black, a mono headline behind a white prompt, Plex Sans for the
// sentence. The home card also carries the landing's mascot. Satori can't
// read CSS variables, so the values below are copied from
// components/landing/landing.css.
const BG = "#0a0a0a"
const SURFACE = "#111111"
const BAR = "#0d0d0d"
const LINE = "#272727"
const TEXT = "#ededed"
const DIM = "#a1a1a1"
const FAINT = "#8a8a8a"
const ACCENT = "#ffffff"
const ACCENT_INK = "#0a0a0a"

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

// Satori reads woff but not woff2, and @fontsource ships both.
async function font(pkg: string, file: string) {
  return readFile(join(process.cwd(), "node_modules/@fontsource", pkg, "files", file))
}

export async function ogImage({ title, description, kicker, mascot = false }: { title: string; description: string; kicker: string; mascot?: boolean }) {
  const [monoSemibold, monoRegular, sansRegular, sansSemibold] = await Promise.all([
    font("ibm-plex-mono", "ibm-plex-mono-latin-600-normal.woff"),
    font("ibm-plex-mono", "ibm-plex-mono-latin-400-normal.woff"),
    font("ibm-plex-sans", "ibm-plex-sans-latin-400-normal.woff"),
    font("ibm-plex-sans", "ibm-plex-sans-latin-600-normal.woff"),
  ])
  const lines = title.split("\n")
  return new ImageResponse(
    <div style={{ display: "flex", width: "100%", height: "100%", padding: 48, background: BG, color: TEXT, fontFamily: "Plex Sans" }}>
      <div style={{ display: "flex", flexDirection: "column", flex: 1, border: `2px solid ${LINE}`, borderRadius: 18, background: SURFACE, overflow: "hidden" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 12, height: 64, padding: "0 28px", background: BAR, borderBottom: `2px solid ${LINE}` }}>
          {[0, 1, 2].map((i) => (
            <div key={i} style={{ display: "flex", width: 14, height: 14, borderRadius: 7, background: LINE }} />
          ))}
          <div style={{ display: "flex", marginLeft: 16, fontFamily: "Plex Mono", fontSize: 24, color: DIM }}>palui.zakiego.com</div>
        </div>

        <div style={{ display: "flex", flexDirection: "column", flex: 1, position: "relative", padding: "40px 56px 44px" }}>
          {mascot ? (
            <svg width="270" height="276" viewBox={MASCOT_VIEWBOX} style={{ position: "absolute", right: 52, bottom: 84 }}>
              {plain(<MascotScene />)}
            </svg>
          ) : null}
          <div style={{ display: "flex", alignItems: "center", gap: 18 }}>
            <svg width="56" height="56" viewBox="0 0 32 32">
              <path d={MARK_TILE} fill={ACCENT} />
              <path d={MARK_CAP} fill={ACCENT_INK} />
              {MARK_EYES.map((e) => (
                <circle key={e.cx} cx={e.cx} cy={e.cy} r={e.r} fill={ACCENT_INK} />
              ))}
              <path d={MARK_SMILE} fill={ACCENT_INK} />
            </svg>
            <div style={{ display: "flex", fontSize: 34, fontWeight: 600, letterSpacing: "-0.01em" }}>Palui Agent</div>
            <div style={{ display: "flex", fontFamily: "Plex Mono", fontSize: 26, color: FAINT, marginLeft: 6 }}>{kicker}</div>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 22, marginTop: "auto" }}>
            {/* A "\n" in the title is a forced line break, so the headline
                breaks where it reads best. */}
            <div
              style={{
                display: "flex",
                flexDirection: "column",
                fontFamily: "Plex Mono",
                fontSize: lines.length > 1 ? 66 : title.length > 32 ? 56 : 72,
                fontWeight: 600,
                letterSpacing: "-0.04em",
                lineHeight: 1.08,
              }}
            >
              {lines.map((line, i) => (
                <div key={line} style={{ display: "flex" }}>
                  <span style={{ color: ACCENT, marginRight: "0.5em", opacity: i === 0 ? 1 : 0 }}>&gt;</span>
                  <span>{line}</span>
                </div>
              ))}
            </div>
            <div style={{ display: "flex", fontSize: 30, color: DIM, lineHeight: 1.4, maxWidth: mascot ? 700 : 960 }}>{description}</div>
          </div>

          <div style={{ display: "flex", marginTop: 32, fontFamily: "Plex Mono", fontSize: 24, color: ACCENT }}>Self-hosted, one owner</div>
        </div>
      </div>
    </div>,
    {
      ...ogSize,
      fonts: [
        { name: "Plex Mono", data: monoSemibold, weight: 600, style: "normal" },
        { name: "Plex Mono", data: monoRegular, weight: 400, style: "normal" },
        { name: "Plex Sans", data: sansRegular, weight: 400, style: "normal" },
        { name: "Plex Sans", data: sansSemibold, weight: 600, style: "normal" },
      ],
    },
  )
}
