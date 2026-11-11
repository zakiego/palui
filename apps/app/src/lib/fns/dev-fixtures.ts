// Dev only: the server side of the fixture toggle (src/components/dev). Both
// functions do nothing outside a dev build, and the fixture code is imported
// lazily behind that check so it stays out of the production bundle.
import { createServerFn } from "@tanstack/react-start"
import { z } from "zod"
import { requireUser, requireWriter } from "@/server/session"

const NAMES = ["demo", "worst", "empty", "one", "huge"] as const

// `current` is null when the database wasn't filled by a fixture, which is
// also what hides the toggle: it only shows on a database a fixture made.
export const getFixtureState = createServerFn({ method: "GET" }).handler(async () => {
  if (!import.meta.env.DEV) return { current: null }
  const { db } = await requireUser()
  const { currentFixture } = await import("@/dev/fixtures")
  return { current: currentFixture(db) }
})

export const setFixture = createServerFn({ method: "POST" })
  .validator(z.object({ name: z.enum(NAMES) }))
  .handler(async ({ data }) => {
    const { db } = await requireWriter()
    if (!import.meta.env.DEV) throw new Error("Fixtures only exist in a dev build.")
    const { applyFixture } = await import("@/dev/fixtures")
    applyFixture(db, data.name)
    return { current: data.name }
  })
