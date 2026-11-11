// Long text from data (a reminder, a run error, an event message) clamped to a
// few lines, with a way to read the rest. The toggle only shows when the text
// is actually cut, which depends on the column's width, so it's measured
// instead of guessed from the character count.
import * as React from "react"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

// Static class names, so Tailwind sees them.
const CLAMP = { 1: "line-clamp-1", 2: "line-clamp-2", 3: "line-clamp-3" } as const

export function ClampText({ text, lines = 2, className }: { text: string; lines?: keyof typeof CLAMP; className?: string }) {
  const t = useT()
  const ref = React.useRef<HTMLSpanElement>(null)
  const [open, setOpen] = React.useState(false)
  const [cut, setCut] = React.useState(false)

  React.useEffect(() => {
    const el = ref.current
    if (!el || open) return
    const measure = () => setCut(el.scrollHeight > el.clientHeight + 1)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [text, open])

  return (
    <>
      {/* Line breaks in the text are kept: a three-line reminder or a stack trace reads as written. */}
      <span ref={ref} dir="auto" className={cn("block text-left break-words whitespace-pre-line", !open && CLAMP[lines], className)}>
        {text}
      </span>
      {(cut || open) && (
        <button
          type="button"
          aria-expanded={open}
          className="mt-0.5 block min-h-6 cursor-pointer rounded-sm text-caption font-medium text-foreground underline decoration-from-font underline-offset-2 outline-none hover:decoration-2 focus-visible:ring-3 focus-visible:ring-ring/50"
          onClick={() => setOpen((o) => !o)}
        >
          {open ? t("common.showLess") : t("common.showMore")}
        </button>
      )}
    </>
  )
}
