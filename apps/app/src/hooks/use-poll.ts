// Load data from a server function inside a component (sheets and dialogs that
// aren't routes), optionally polling while open. Stale responses are dropped.
import * as React from "react"

export function usePoll<T>(load: (() => Promise<T>) | null, opts: { intervalMs?: number; deps?: React.DependencyList } = {}) {
  const [data, setData] = React.useState<T | undefined>(undefined)
  const [error, setError] = React.useState<string | undefined>(undefined)
  const [loading, setLoading] = React.useState(false)
  const seq = React.useRef(0)
  const loadRef = React.useRef(load)
  loadRef.current = load

  const refresh = React.useCallback(async () => {
    const fn = loadRef.current
    if (!fn) return
    const mine = ++seq.current
    setLoading(true)
    try {
      const next = await fn()
      if (mine === seq.current) {
        setData(next)
        setError(undefined)
      }
    } catch (err) {
      if (mine === seq.current) setError(err instanceof Error ? err.message : String(err))
    } finally {
      if (mine === seq.current) setLoading(false)
    }
  }, [])

  const enabled = !!load
  React.useEffect(() => {
    if (!enabled) return
    void refresh()
    if (!opts.intervalMs) return
    const timer = setInterval(() => void refresh(), opts.intervalMs)
    return () => {
      clearInterval(timer)
      seq.current++
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, opts.intervalMs, refresh, ...(opts.deps ?? [])])

  return { data, error, loading, refresh, setData }
}
