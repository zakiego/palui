// The turn bus: in-process fan-out of TurnEvents to the web chat SSE stream and
// any dashboard transcript. One per process, like the event listeners.
import type { TurnEvent } from "../control"

type Listener = (e: TurnEvent) => void

export type TurnBus = {
  publish(e: TurnEvent): void
  subscribe(fn: Listener): () => void
  size(): number
}

export function createTurnBus(): TurnBus {
  const listeners = new Set<Listener>()
  return {
    publish(e) {
      for (const l of listeners) {
        try {
          l(e)
        } catch {
          // A slow or broken subscriber must never break a turn.
        }
      }
    },
    subscribe(fn) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    size: () => listeners.size,
  }
}

export const turnBus: TurnBus = createTurnBus()
