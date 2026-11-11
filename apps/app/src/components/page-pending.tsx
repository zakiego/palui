// What a page shows while its loader waits on the server: the frame every
// route's pendingComponent renders in, plus the pieces the skeletons are built
// from. Each piece is the real component (PageHeader's layout, Card, Table),
// so the placeholder has the page's edges and row heights and nothing jumps
// when the data lands.
import * as React from "react"

import { Card } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useT, type Key } from "@/lib/i18n"
import { cn } from "@/lib/utils"

/**
 * The router shows this only once a load takes longer than 300 ms, so it
 * fades in rather than replacing the old page in one frame. When it unmounts,
 * the page that replaces it fades in too.
 *
 * That second fade is the only route-level motion in the dashboard, and it's
 * tied to the skeleton on purpose: a fast page switch never shows a skeleton,
 * so it never fades. Only a load slow enough to need a placeholder gets one,
 * and there the fade turns "grey blocks, then a page" into one change.
 */
/** The id of the polite live region the shell keeps mounted for page loads. */
export const PAGE_STATUS_ID = "page-status"

export function PagePending({ page, className, children }: { page: Key; className?: string; children: React.ReactNode }) {
  const t = useT()
  // A layout effect, so the fade starts in the commit that swaps the skeleton
  // for the page, before the browser paints the page at full opacity.
  React.useLayoutEffect(() => revealMain, [])
  // Screen readers announce a change inside a live region that was already
  // there, not a region inserted with its text, so the words go into the
  // shell's PAGE_STATUS_ID region (always mounted) and leave with the skeleton.
  // The shell's own skeleton has no shell yet, so it stays silent.
  React.useEffect(() => {
    const region = document.getElementById(PAGE_STATUS_ID)
    if (!region) return
    region.textContent = t("common.loadingPage", { page: t(page) })
    return () => {
      region.textContent = ""
    }
  }, [t, page])
  return <div className={cn("pending-in flex flex-col gap-6", className)}>{children}</div>
}

function revealMain() {
  // Reduced motion: everything in the dashboard is instant (DESIGN.md).
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return
  const fade = (main: HTMLElement | null) => {
    if (typeof main?.animate !== "function") return false
    // From 0.4, not 0: the skeleton is already gone in this frame, and a page
    // starting from nothing would dip to an empty screen first.
    main.animate([{ opacity: 0.4 }, { opacity: 1 }], { duration: 200, easing: "cubic-bezier(0.2, 0, 0, 1)" })
    return true
  }
  // The shell's skeleton unmounts before <main> is in the document, so try
  // again once the commit that inserts it is done (still before paint).
  if (!fade(document.getElementById("main-content"))) queueMicrotask(() => void fade(document.getElementById("main-content")))
}

/**
 * One line of text at the size of the role it stands in for: the line box is
 * the real line height (pass the text classes, like `text-sm`), and the bar
 * inside is a little shorter, so a column of them reads as text, not as tiles.
 */
export function SkeletonText({ className, width }: { className: string; width: string }) {
  return (
    <span className={cn("flex h-lh max-w-full items-center", className)}>
      <Skeleton className={cn("h-[0.8em] max-w-full rounded-md", width)} />
    </span>
  )
}

/** A Button-sized block (default size, 40 px on touch, like the real one). */
export function SkeletonButton({ className }: { className?: string }) {
  return <Skeleton className={cn("h-8 shrink-0 pointer-coarse:h-10", className)} />
}

/**
 * The page title, its description and the action, laid out the way PageHeader
 * lays them out: the action wraps under the text on a phone, where the
 * description also takes two lines.
 */
export function PageHeaderSkeleton({
  title = "w-32",
  description = "w-96",
  wraps = true,
  action,
}: {
  title?: string
  description?: string
  /** False when the real description fits one line on a phone. */
  wraps?: boolean
  action?: string
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
      <div className="flex min-w-0 flex-col gap-1.5">
        <SkeletonText className="font-heading text-title" width={title} />
        <span className="flex flex-col">
          <SkeletonText className="text-sm" width={description} />
          {wraps && <SkeletonText className="text-sm sm:hidden" width="w-1/2" />}
        </span>
      </div>
      {action && <SkeletonButton className={action} />}
    </div>
  )
}

/** A TabsList-sized block (32 px, 40 px on touch, like the real one). */
export function TabsSkeleton({ className }: { className: string }) {
  return <Skeleton className={cn("h-8 max-w-full pointer-coarse:h-10", className)} />
}

export type SkeletonColumn = {
  /** The real column's width and breakpoint classes, set on both the head and the cells. */
  className?: string
  /** The header label's width; leave out for a column with no visible label (actions). */
  head?: string
  /** A fixed width for the first line (a date, a count) instead of varied text. */
  short?: string
  /** The first line's text classes. Defaults to body text. */
  text?: string
  /**
   * Shorter lines under the first (a schedule, a badge row, a zone). `true`
   * for one line; class strings for lines that only show at some widths.
   */
  sub?: true | string | string[]
  /** A pill instead of a line (a status badge). */
  badge?: boolean
  /** Controls at the end of the row: one block per entry (its size classes), right-aligned. */
  controls?: string[]
  align?: "end"
}

const subLines = (sub: SkeletonColumn["sub"]): string[] => (sub === undefined ? [] : sub === true ? [""] : typeof sub === "string" ? [sub] : sub)

// Text lines of different lengths, so the rows read as text and not as a grid of bars.
const LINE_WIDTHS = ["w-3/5", "w-4/5", "w-2/5", "w-3/4", "w-1/2", "w-2/3"]
const SUB_WIDTHS = ["w-28", "w-20", "w-32", "w-24", "w-28", "w-16"]

/**
 * A placeholder that is the real Card and Table the page renders, so the
 * header, row heights, padding and card edge match the page that replaces it.
 */
export function TableSkeleton({ columns, rows = 6 }: { columns: SkeletonColumn[]; rows?: number }) {
  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Table>
        <TableHeader>
          <TableRow>
            {columns.map((c, i) => (
              <TableHead key={i} className={c.className}>
                {c.head && <Skeleton className={cn("h-2.5", c.head, c.align === "end" && "ms-auto")} />}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {Array.from({ length: rows }, (_, r) => (
            <TableRow key={r} className="hover:bg-transparent">
              {columns.map((c, i) => (
                <TableCell key={i} className={c.className}>
                  {c.controls ? (
                    <span className="flex items-center justify-end gap-2">
                      {c.controls.map((size, k) => (
                        <Skeleton key={k} className={cn("shrink-0", size)} />
                      ))}
                    </span>
                  ) : (
                    <span className={cn("flex flex-col gap-1", c.align === "end" && "items-end")}>
                      {c.badge ? (
                        <Skeleton className={cn("h-5 rounded-full", c.short ?? "w-14")} />
                      ) : (
                        <SkeletonText className={c.text ?? "text-sm"} width={c.short ?? LINE_WIDTHS[(r + i) % LINE_WIDTHS.length]} />
                      )}
                      {subLines(c.sub).map((cls, k) => (
                        <SkeletonText key={k} className={cn("text-caption", cls)} width={SUB_WIDTHS[(r + i + k) % SUB_WIDTHS.length]} />
                      ))}
                    </span>
                  )}
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  )
}
