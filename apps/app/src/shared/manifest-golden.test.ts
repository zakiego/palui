// The dashboard's connector manifests (the forms) and the Go server's (what it
// validates and stores) are two copies. Both are checked against the same
// frozen golden, apps/server/testdata/connectors.json: the Go registry test on
// its side, this test here. A manifest change updates both copies and the
// golden in the same change.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"
import { manifestList } from "./connectors/manifests"

type Zodish = { safeParse(v: unknown): { success: boolean; data?: unknown; error?: { issues: unknown[] } }; shape?: Record<string, unknown> }

const golden = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../../server/testdata/connectors.json"), "utf8")) as {
  manifests: Record<string, unknown>[]
}

// The same projection the golden generator wrote.
function project(m: (typeof manifestList)[number]) {
  const { config, secrets, ...plain } = m as unknown as Record<string, unknown> & { config: Zodish; secrets: Record<string, Zodish> }
  const parsed = config.safeParse({})
  return JSON.parse(
    JSON.stringify({
      ...plain,
      configKeys: Object.keys(config.shape ?? {}),
      configDefaults: parsed.success ? { ok: true, value: parsed.data } : { ok: false, issues: parsed.error!.issues },
      secretFields: Object.entries(secrets).map(([name, s]) => ({ name, optional: s.safeParse(undefined).success })),
    }),
  )
}

describe("connector manifests", () => {
  it("list the golden's connectors in the golden's order", () => {
    expect(manifestList.map((m) => m.id)).toEqual(golden.manifests.map((m) => m.id))
  })

  for (const m of manifestList) {
    it(`${m.id} matches the golden`, () => {
      expect(project(m)).toEqual(golden.manifests.find((g) => g.id === m.id))
    })
  }
})
