// The line under a list that shows only part of what exists. Lists here are
// capped by the query, so the note says how many rows there really are instead
// of letting the visible count pass for the total.
import { useT } from "@/lib/i18n"

export function ListCapNote({ shown, total }: { shown: number; total: number }) {
  const t = useT()
  if (total <= shown) return null
  return (
    <p className="border-t px-4 py-3 text-center text-caption text-muted-foreground tabular-nums">
      {t("list.capped", { shown: shown.toLocaleString("en"), total: total.toLocaleString("en") })}
    </p>
  )
}
