// The dashboard's signed-in viewer, read by the shell's route guard.
import { getSession } from "@/lib/fns/session"
import { createViewerCache } from "@/lib/viewer-cache"

const loadViewer = async () => (await getSession()).viewer

const cache = createViewerCache(loadViewer)

export function currentViewer() {
  // The server renders for many users from one process, so it never remembers one.
  return typeof window === "undefined" ? loadViewer() : cache.get()
}

export const seedViewer = cache.seed
export const forgetViewer = cache.clear
export const onViewerLost = cache.onLost
