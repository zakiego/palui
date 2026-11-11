// A connector's icon on a small square tile, so a node reads at a glance
// (TASTE.md: an icon on every node). shadcn has no tile (ItemMedia's icon
// variant is a bare glyph), so this is the one place it's drawn. Palui itself
// is PaluiMark, which brings its own tile.
import type * as React from "react"
import { cn } from "@/lib/utils"

export function IconTile({ children, size = "md", className }: { children: React.ReactNode; size?: "sm" | "md" | "lg"; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "flex shrink-0 items-center justify-center [&_svg]:pointer-events-none",
        size === "sm" && "size-8 rounded-md [&_svg:not([class*='size-'])]:size-4",
        size === "md" && "size-9 rounded-lg [&_svg:not([class*='size-'])]:size-4",
        size === "lg" && "size-10 rounded-lg [&_svg:not([class*='size-'])]:size-6",
        "bg-muted text-foreground",
        className,
      )}
    >
      {children}
    </span>
  )
}
