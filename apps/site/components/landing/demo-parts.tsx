import { ArrowUp, Check } from "lucide-react"
import { MARK_TILE, PaluiFace } from "@/components/palui-mark"

// The small drawn parts the landing's demos share. A demo is a picture
// (`role="img"` with a text alternative), so nothing here is a real control.

export function AgentMark({ size = 24 }: { size?: number }) {
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden="true" className="lp-agent-mark">
      <path d={MARK_TILE} className="lp-mark-tile" />
      <PaluiFace className="lp-mark-face" />
    </svg>
  )
}

export type StepState = "pending" | "active" | "done"

/** A hollow ring while it waits, a turning ring while it works, a check when done. */
export function Status({ state }: { state: StepState }) {
  if (state === "done") return <Check className="lp-status-done" aria-hidden="true" />
  return <span className="lp-status" data-state={state} aria-hidden="true" />
}

/** The approve and deny pair. The picture presses Approve itself. */
export function Approval({ pressed }: { pressed: boolean }) {
  return (
    <span className="lp-actions">
      <span className="lp-btn lp-btn-yes" data-pressed={pressed ? "" : undefined}>
        Approve
      </span>
      <span className="lp-btn lp-btn-no">Deny</span>
    </span>
  )
}

/** A message box that fills in as the line is typed. `sent` keeps the line in it, dimmed, once it has gone. */
export function Composer({ text, placeholder, sent = false, className }: { text: string; placeholder: string; sent?: boolean; className?: string }) {
  return (
    <div className={className ? `lp-composer ${className}` : "lp-composer"}>
      <span className="lp-composer-text" data-empty={text ? undefined : ""} data-sent={sent ? "" : undefined}>
        {text || placeholder}
      </span>
      <span className="lp-send" data-ready={text && !sent ? "" : undefined}>
        <ArrowUp aria-hidden="true" />
      </span>
    </div>
  )
}
