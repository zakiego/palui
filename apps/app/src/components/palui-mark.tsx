// Palui's mark: the mascot's head on an ink tile, the same drawing as the
// favicon. apps/site/scripts/brand-icons.mjs writes that file, so the mark here
// can't drift from the tab icon or the site. It carries its own tile and
// colors, so it looks the same in light and dark and needs no backdrop.
import favicon from "../../public/app/favicon.svg?raw"
import { cn } from "@/lib/utils"

export function PaluiMark({ className }: { className?: string }) {
  return <span aria-hidden="true" className={cn("block size-8 shrink-0 [&>svg]:size-full", className)} dangerouslySetInnerHTML={{ __html: favicon }} />
}
