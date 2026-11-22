// The `prototype` skill's variant picker (its PICKER.md), kept on the Connectors
// page until the owner confirms the Explore pick. It shows only when the URL has
// `?v=<n>`: 1-N and the arrow keys switch, the choice lives in the URL. The
// variant swap itself is instant.
import * as React from "react"
import "./prototype-picker.css"

export function PrototypePicker({ names, active, onChange }: { names: string[]; active: number; onChange: (index: number) => void }) {
  const highlight = React.useRef<HTMLSpanElement>(null)
  const items = React.useRef<(HTMLButtonElement | null)[]>([])
  const [ready, setReady] = React.useState(false)

  const move = React.useCallback(() => {
    const el = items.current[active]
    if (!el || !highlight.current) return
    highlight.current.style.width = `${el.offsetWidth}px`
    highlight.current.style.transform = `translateX(${el.offsetLeft}px)`
  }, [active])

  React.useLayoutEffect(move, [move])

  React.useEffect(() => {
    window.addEventListener("resize", move)
    return () => window.removeEventListener("resize", move)
  }, [move])

  // Enable the slide only after first paint, so load doesn't animate.
  React.useEffect(() => {
    let inner = 0
    const outer = requestAnimationFrame(() => {
      inner = requestAnimationFrame(() => setReady(true))
    })
    return () => {
      cancelAnimationFrame(outer)
      cancelAnimationFrame(inner)
    }
  }, [])

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      if (target && (/^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName) || target.isContentEditable)) return
      // Keys typed inside an open sheet or dialog belong to it, not the picker.
      if (target?.closest("[role=dialog], [role=alertdialog]")) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const num = Number.parseInt(e.key, 10)
      if (num >= 1 && num <= names.length) onChange(num - 1)
      else if (e.key === "ArrowRight") onChange((active + 1) % names.length)
      else if (e.key === "ArrowLeft") onChange((active - 1 + names.length) % names.length)
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [active, names.length, onChange])

  return (
    <nav className="proto-picker" aria-label="Prototype variants" data-ready={ready ? "" : undefined}>
      <span ref={highlight} className="proto-picker-highlight" aria-hidden="true" />
      {names.map((name, i) => (
        <button
          key={name}
          type="button"
          ref={(el) => {
            items.current[i] = el
          }}
          className="proto-picker-item"
          data-active={i === active ? "" : undefined}
          aria-current={i === active ? "true" : undefined}
          onClick={() => onChange(i)}
        >
          {name}
        </button>
      ))}
    </nav>
  )
}
