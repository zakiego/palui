// Palui's mark: a switch, for Saklar. Drawn in currentColor.
export function PaluiMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <rect x="5" y="10" width="22" height="12" rx="6" fill="none" stroke="currentColor" strokeWidth="2.5" />
      <circle cx="21" cy="16" r="3" fill="currentColor" />
    </svg>
  )
}
