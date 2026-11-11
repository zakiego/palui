// The three Saklar layouts from the PLAN 11.7 Explore, in picker order. The
// default is Claude's pick until Zaki confirms it (DESIGN.md, Decisions); the
// Home map follows the same pick.
import { SaklarBoard, SaklarBoardSkeleton } from "./variants/board"
import { SaklarGrid, SaklarGridSkeleton } from "./variants/grid"
import { SaklarRows, SaklarRowsSkeleton } from "./variants/rows"

export const SAKLAR_LAYOUTS = [
  { id: "grid", name: "Cards", Layout: SaklarGrid, Skeleton: SaklarGridSkeleton },
  { id: "rows", name: "Rows", Layout: SaklarRows, Skeleton: SaklarRowsSkeleton },
  { id: "board", name: "Board", Layout: SaklarBoard, Skeleton: SaklarBoardSkeleton },
] as const

// Index into SAKLAR_LAYOUTS. Cards: see DESIGN.md "Saklar layout".
export const DEFAULT_SAKLAR_LAYOUT = 0
