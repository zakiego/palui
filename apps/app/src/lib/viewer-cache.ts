// Remembers who is signed in, per browser tab, so a page switch doesn't wait on
// a session round trip before it can start.
//
// This is only the dashboard's own redirect to /login. It grants nothing: every
// RPC and /api route checks the session itself, so a stale entry
// can at most show data this tab had already loaded.

/** Younger than this, the entry is used as is. */
export const FRESH_MS = 15_000
/** Older than this, the entry is not trusted and the caller waits for the server. */
export const MAX_AGE_MS = 10 * 60_000

export type ViewerCache<V> = {
  /** The viewer, or null when nobody is signed in. Between FRESH_MS and MAX_AGE_MS it answers from memory and rechecks behind the scenes. */
  get: (now?: number) => Promise<V | null>
  /** Stores a viewer the server already verified (the one the page was rendered with). Keeps a newer entry. */
  seed: (viewer: V, now?: number) => void
  /** Drops the entry, for sign-in and sign-out. */
  clear: () => void
  /** Calls `fn` when a recheck finds the session gone. Returns the unsubscribe. */
  onLost: (fn: () => void) => () => void
}

export function createViewerCache<V>(load: () => Promise<V | null>): ViewerCache<V> {
  let entry: { viewer: V; at: number } | undefined
  let pending: Promise<V | null> | undefined
  // Bumped by clear(), so a load that was already on its way can't write the old answer back.
  let generation = 0
  const lostListeners = new Set<() => void>()

  const refresh = (now: number) => {
    if (pending) return pending
    const startedIn = generation
    const hadViewer = entry !== undefined
    const request: Promise<V | null> = load()
      .then((viewer) => {
        if (startedIn !== generation) return viewer
        entry = viewer ? { viewer, at: now } : undefined
        if (!viewer && hadViewer) for (const fn of lostListeners) fn()
        return viewer
      })
      .finally(() => {
        if (pending === request) pending = undefined
      })
    pending = request
    return request
  }

  return {
    get(now = Date.now()) {
      if (!entry) return refresh(now)
      const age = now - entry.at
      if (age >= MAX_AGE_MS) return refresh(now)
      // A failed recheck (offline, a restart) keeps the entry; the next one tries again.
      if (age >= FRESH_MS) refresh(now).catch(() => {})
      return Promise.resolve(entry.viewer)
    },
    seed(viewer, now = Date.now()) {
      if (!entry) entry = { viewer, at: now }
    },
    clear() {
      generation++
      entry = undefined
      pending = undefined
    },
    onLost(fn) {
      lostListeners.add(fn)
      return () => {
        lostListeners.delete(fn)
      }
    },
  }
}
