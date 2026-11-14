// The dashboard is a static SPA that talks to the Go server (apps/server) over
// JSON. Two checks: a walk of the import graph from the entry (src/main.tsx)
// resolves every local import, and a production build in memory keeps the dev
// fixture toggle out and serves hashed assets from /app/assets.
import { existsSync, readFileSync, statSync } from "node:fs"
import { dirname, join, relative, resolve } from "node:path"
import { describe, expect, it } from "vitest"

const APP = resolve(import.meta.dirname, "..")
const SRC = join(APP, "src")
const ENTRY = join(SRC, "main.tsx")

const IMPORT_RE = /(?:^|[\s;])(?:import|export)\s+(?:type\s+)?(?:[^'"]*?\s+from\s+)?["']([^"']+)["']|import\(\s*["']([^"']+)["']\s*\)/g

function specifiers(text: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(IMPORT_RE)) {
    const spec = m[1] ?? m[2]
    if (spec) out.push(spec)
  }
  return out
}

function resolveFile(from: string, spec: string): string | null {
  const bare = spec.replace(/\?.*$/, "")
  let base: string
  if (bare.startsWith("@/")) base = join(SRC, bare.slice(2))
  else if (bare.startsWith(".")) base = resolve(dirname(from), bare)
  else return null
  for (const candidate of [base, `${base}.ts`, `${base}.tsx`, join(base, "index.ts"), join(base, "index.tsx")]) {
    if (existsSync(candidate) && statSync(candidate).isFile()) return candidate
  }
  throw new Error(`${relative(APP, from)}: can't resolve ${spec}`)
}

// Every source file reachable from the entry (type-only imports included:
// the SPA's types must not come from the backend either), and every bare
// package it names.
function walk(entry: string) {
  const files = new Set<string>()
  const packages = new Map<string, string>()
  const edges: { from: string; to: string }[] = []
  const queue = [entry]
  while (queue.length) {
    const file = queue.pop()!
    if (files.has(file)) continue
    files.add(file)
    if (!/\.(ts|tsx)$/.test(file)) continue
    for (const spec of specifiers(readFileSync(file, "utf8"))) {
      const target = resolveFile(file, spec)
      if (target === null) {
        packages.set(spec, relative(APP, file))
        continue
      }
      edges.push({ from: relative(APP, file), to: relative(APP, target) })
      queue.push(target)
    }
  }
  return { files: [...files].map((f) => relative(APP, f)), packages, edges }
}

describe("SPA import graph", () => {
  const graph = walk(ENTRY)

  it("reaches the pages", () => {
    expect(graph.files).toContain("src/routes/_app/settings.tsx")
    expect(graph.files).toContain("src/lib/api/client.ts")
  })
})

describe("SPA production bundle", () => {
  it("leaves the dev toggle out and hashes its assets", async () => {
    const { build } = await import("vite")
    // Vitest runs with NODE_ENV=test, which would make this a dev build
    // (import.meta.env.DEV true). `pnpm build` runs with production.
    const nodeEnv = process.env.NODE_ENV
    process.env.NODE_ENV = "production"
    let result: Awaited<ReturnType<typeof build>>
    try {
      result = await build({ root: APP, configFile: join(APP, "vite.config.ts"), mode: "production", logLevel: "silent", build: { write: false } })
    } finally {
      process.env.NODE_ENV = nodeEnv
    }
    const outputs = (Array.isArray(result) ? result : [result]).flatMap((r) => ("output" in r ? r.output : []))
    const ids = outputs.flatMap((o) => (o.type === "chunk" ? o.moduleIds : [])).map((id) => id.replace(/\\/g, "/"))
    const html = outputs.flatMap((o) => (o.type === "asset" && o.fileName === "index.html" ? [String(o.source)] : []))[0] ?? ""
    expect(ids.length).toBeGreaterThan(100)
    const local = ids.filter((id) => id.startsWith(`${APP}/`)).map((id) => relative(APP, id.replace(/\?.*$/, "")))
    // The dev fixture toggle is only imported in a dev build.
    expect(local.filter((id) => id.startsWith("src/components/dev/") || id === "src/lib/api/dev-fixtures.ts")).toEqual([])
    // Assets are hashed and served from /app/assets.
    expect(html).toMatch(/<script type="module" crossorigin src="\/app\/assets\/index-[\w-]+\.js"><\/script>/)
    expect(html).toMatch(/<link rel="stylesheet" crossorigin href="\/app\/assets\/index-[\w-]+\.css">/)
  })
})
