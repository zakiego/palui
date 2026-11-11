import { createFromSource } from "fumadocs-core/search/server"
import { source } from "@/lib/source"

// The static search index, exported as out/search.json. It isn't at
// Fumadocs' default /api/search because the edge Worker sends every /api path
// to the app on palui.zakiego.com.
export const revalidate = false

export const { staticGET: GET } = createFromSource(source, {
  language: "english",
})
