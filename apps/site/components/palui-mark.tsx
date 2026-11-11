import { cn } from "@/lib/cn"

// Palui's mark: an original geometric drawing, not the comic's art. Si Palui,
// the Banjar folk hero, wears a kopiah (the rimless cap), so the mark is a
// kopiah over two dots and a satisfied smile on a plain tile. Every shape is
// filled (no strokes) so it renders the same in browsers, Satori and
// ImageMagick. The same paths are in lib/og.tsx. The favicons are the
// mascot's head instead (scripts/favicon.mjs).
export const MARK_TILE = "M0 8a8 8 0 0 1 8-8h16a8 8 0 0 1 8 8v16a8 8 0 0 1-8 8H8a8 8 0 0 1-8-8Z"
export const MARK_CAP =
  "M10.2 6h11.6a1.2 1.2 0 0 1 1.17.93l1.5 6.6a1.2 1.2 0 0 1-1.17 1.47H8.7a1.2 1.2 0 0 1-1.17-1.47l1.5-6.6A1.2 1.2 0 0 1 10.2 6Z"
export const MARK_SMILE =
  "M10.62 21.43a1.3 1.3 0 0 1 1.81-.28Q16 23.8 19.57 21.15a1.3 1.3 0 1 1 1.53 2.1Q16 27 10.9 23.25a1.3 1.3 0 0 1-.28-1.82Z"
export const MARK_EYES = [
  { cx: 12.4, cy: 18.2, r: 1.5 },
  { cx: 19.6, cy: 18.2, r: 1.5 },
]

/** The face alone (cap, eyes, smile), for drawing inside another shape. */
export function PaluiFace({ className }: { className?: string }) {
  return (
    <g className={className}>
      <path d={MARK_CAP} />
      {MARK_EYES.map((e) => (
        <circle key={e.cx} {...e} />
      ))}
      <path d={MARK_SMILE} />
    </g>
  )
}

export function PaluiMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className={cn("size-6 shrink-0", className)}>
      <path d={MARK_TILE} className="fill-primary" />
      <PaluiFace className="fill-primary-foreground" />
    </svg>
  )
}
