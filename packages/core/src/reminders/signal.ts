// "Something on the schedule changed": reminder and job writes call
// notifyScheduleChanged() so the scheduler re-arms its timer right away instead of
// waiting for the next sweep. In-process only (the runtime owns the scheduler; the
// app pokes the runtime through the control API).
type Listener = () => void
const listeners = new Set<Listener>()

export function onScheduleChanged(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function notifyScheduleChanged() {
  for (const l of listeners) {
    try {
      l()
    } catch {
      // A listener must never break the writer.
    }
  }
}
