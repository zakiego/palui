// The three Connectors page layouts from the PLAN 11.7 Explore, in picker order. The
// default is Claude's pick until Zaki confirms it (DESIGN.md, Decisions); the
// Home map follows the same pick.
import { ConnectorBoard, ConnectorBoardSkeleton } from "./variants/board"
import { ConnectorGrid, ConnectorGridSkeleton } from "./variants/grid"
import { ConnectorRows, ConnectorRowsSkeleton } from "./variants/rows"

export const CONNECTOR_LAYOUTS = [
  { id: "grid", name: "Cards", Layout: ConnectorGrid, Skeleton: ConnectorGridSkeleton },
  { id: "rows", name: "Rows", Layout: ConnectorRows, Skeleton: ConnectorRowsSkeleton },
  { id: "board", name: "Board", Layout: ConnectorBoard, Skeleton: ConnectorBoardSkeleton },
] as const

// Index into CONNECTOR_LAYOUTS. Cards: see DESIGN.md "Connectors page layout".
export const DEFAULT_CONNECTOR_LAYOUT = 0
