// One EventSource per tab on /api/events/stream, shared through context. The
// header reads the summary; pages subscribe to rows to refresh what they show.
import * as React from "react"

export type LiveEvent = { id: number; at: number; level: string; connectorId: string | null; kind: string; message: string }
export type LiveSummary = { total: number; on: number; attention: { id: string; label: string; status: string }[] }

type Ctx = {
  summary: LiveSummary | null
  connected: boolean
  subscribe: (fn: (rows: LiveEvent[]) => void) => () => void
}

const LiveContext = React.createContext<Ctx>({ summary: null, connected: false, subscribe: () => () => {} })

export function LiveEventsProvider({ initial, children }: { initial: LiveSummary; children: React.ReactNode }) {
  const [summary, setSummary] = React.useState<LiveSummary | null>(initial)
  const [connected, setConnected] = React.useState(false)
  const listeners = React.useRef(new Set<(rows: LiveEvent[]) => void>())

  React.useEffect(() => {
    if (typeof EventSource === "undefined") return
    const es = new EventSource("/api/events/stream")
    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)
    es.addEventListener("summary", (e) => {
      try {
        setSummary(JSON.parse((e as MessageEvent).data) as LiveSummary)
      } catch {
        // a malformed frame is skipped
      }
    })
    es.addEventListener("events", (e) => {
      try {
        const rows = JSON.parse((e as MessageEvent).data) as LiveEvent[]
        for (const fn of listeners.current) fn(rows)
      } catch {
        // a malformed frame is skipped
      }
    })
    return () => es.close()
  }, [])

  const subscribe = React.useCallback((fn: (rows: LiveEvent[]) => void) => {
    listeners.current.add(fn)
    return () => {
      listeners.current.delete(fn)
    }
  }, [])

  const value = React.useMemo(() => ({ summary, connected, subscribe }), [summary, connected, subscribe])
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>
}

export function useLiveSummary() {
  return React.useContext(LiveContext).summary
}

// Calls `fn` for each batch of new rows that match `filter`, debounced so a burst
// of status changes triggers one refresh.
export function useLiveRefresh(fn: () => void, filter: (e: LiveEvent) => boolean = () => true, delayMs = 400) {
  const { subscribe } = React.useContext(LiveContext)
  const fnRef = React.useRef(fn)
  const filterRef = React.useRef(filter)
  fnRef.current = fn
  filterRef.current = filter
  React.useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    const off = subscribe((rows) => {
      if (!rows.some((r) => filterRef.current(r))) return
      clearTimeout(timer)
      timer = setTimeout(() => fnRef.current(), delayMs)
    })
    return () => {
      clearTimeout(timer)
      off()
    }
  }, [subscribe, delayMs])
}
