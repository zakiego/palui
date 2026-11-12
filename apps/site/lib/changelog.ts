// Parses and checks content/docs/changelog.mdx, the one changelog. The site test
// (changelog.test.ts) runs the check on every `pnpm test`, and scripts/release.ts
// uses the same code to cut a release, so this file imports nothing.
//
// Shape it expects:
//   ## Unreleased                 always first, "Nothing yet." when empty
//   ## 0.2.0 (2026-11-20)         one per release, newest first
//   ## Not in v1                  any other h2 that isn't version-like is free text

export type Release = { version: string; date: string; body: string }
export type Changelog = { unreleased: string | null; releases: Release[] }

export const EMPTY_UNRELEASED = "Nothing yet."

const RELEASE_HEADING = /^## (\d+\.\d+\.\d+) \((\d{4}-\d{2}-\d{2})\)$/
// Anything that starts like a version but isn't the exact shape above.
const VERSION_LIKE = /^## v?\d/
const SEMVER = /^(\d+)\.(\d+)\.(\d+)$/

type Section = { heading: string; body: string[]; line: number }

function sections(src: string): Section[] {
  const out: Section[] = []
  let current: Section | null = null
  let fenced = false
  const lines = src.split("\n")
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]!
    if (line.startsWith("```")) fenced = !fenced
    if (!fenced && line.startsWith("## ")) {
      current = { heading: line.trimEnd(), body: [], line: i + 1 }
      out.push(current)
    } else if (current) {
      current.body.push(line)
    }
  }
  return out
}

export function parseChangelog(src: string): Changelog {
  let unreleased: string | null = null
  const releases: Release[] = []
  for (const s of sections(src)) {
    const body = s.body.join("\n").trim()
    if (s.heading === "## Unreleased") unreleased = body
    const m = RELEASE_HEADING.exec(s.heading)
    if (m) releases.push({ version: m[1]!, date: m[2]!, body })
  }
  return { unreleased, releases }
}

export function compareVersions(a: string, b: string): number {
  const pa = SEMVER.exec(a)
  const pb = SEMVER.exec(b)
  if (!pa || !pb) throw new Error(`not a version: ${pa ? b : a}`)
  for (let i = 1; i <= 3; i++) {
    const d = Number(pa[i]) - Number(pb[i])
    if (d !== 0) return d
  }
  return 0
}

export type Bump = "major" | "minor" | "patch"

export function bumpVersion(version: string, bump: Bump): string {
  const m = SEMVER.exec(version)
  if (!m) throw new Error(`not a version: ${version}`)
  const [major, minor, patch] = [Number(m[1]), Number(m[2]), Number(m[3])]
  if (bump === "major") return `${major + 1}.0.0`
  if (bump === "minor") return `${major}.${minor + 1}.0`
  return `${major}.${minor}.${patch + 1}`
}

function validDate(date: string): boolean {
  const d = new Date(`${date}T00:00:00Z`)
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === date
}

// Every rule the changelog must hold, as readable problems. Empty means fine.
// `version` is the root package.json version.
export function checkChangelog(src: string, version: string): string[] {
  const problems: string[] = []
  const all = sections(src)
  const unreleasedAt = all.findIndex((s) => s.heading === "## Unreleased")
  if (unreleasedAt === -1) problems.push("missing the `## Unreleased` section")
  if (all.filter((s) => s.heading === "## Unreleased").length > 1) problems.push("more than one `## Unreleased` section")

  let prev: { version: string; date: string } | null = null
  for (const [i, s] of all.entries()) {
    const m = RELEASE_HEADING.exec(s.heading)
    if (!m) {
      if (VERSION_LIKE.test(s.heading)) problems.push(`line ${s.line}: "${s.heading}" should look like "## 1.2.3 (YYYY-MM-DD)"`)
      continue
    }
    const [, v, date] = m as unknown as [string, string, string]
    if (unreleasedAt === -1 || i < unreleasedAt) problems.push(`line ${s.line}: ${v} comes before \`## Unreleased\``)
    if (!validDate(date)) problems.push(`line ${s.line}: ${date} is not a real date`)
    if (!s.body.some((l) => /^\s*- \S/.test(l))) problems.push(`line ${s.line}: ${v} has no bullet points`)
    if (prev) {
      if (compareVersions(v, prev.version) >= 0) problems.push(`line ${s.line}: ${v} is listed below ${prev.version}, so it must be older`)
      if (date > prev.date) problems.push(`line ${s.line}: ${v} is dated after ${prev.version}`)
    }
    prev = { version: v, date }
  }

  const { releases } = parseChangelog(src)
  const latest = releases[0]
  if (!latest) problems.push("no release yet: add one, e.g. `## 0.1.0 (YYYY-MM-DD)`")
  else if (latest.version !== version)
    problems.push(`package.json says ${version} but the newest changelog release is ${latest.version}. Release through the release PR (\`pnpm release open\`), never bump by hand.`)
  return problems
}

// Moves the Unreleased notes under a new release heading and leaves
// Unreleased empty. Throws when there's nothing to release.
export function cutRelease(src: string, version: string, date: string): string {
  const { unreleased } = parseChangelog(src)
  if (unreleased === null) throw new Error("missing the `## Unreleased` section")
  if (!unreleased || unreleased === EMPTY_UNRELEASED || !/^\s*- \S/m.test(unreleased))
    throw new Error("`## Unreleased` has no bullet points, so there's nothing to release")
  const lines = src.split("\n")
  const start = lines.findIndex((l) => l.trimEnd() === "## Unreleased")
  let end = start + 1
  while (end < lines.length && !lines[end]!.startsWith("## ")) end++
  const next = [
    "## Unreleased",
    "",
    EMPTY_UNRELEASED,
    "",
    `## ${version} (${date})`,
    "",
    unreleased,
    "",
  ]
  return [...lines.slice(0, start), ...next, ...lines.slice(end)].join("\n")
}

export type Suggestion = { bump: Bump | null; reason: string }

// Picks the bump from the Unreleased notes, the same way a person would:
// any `### Breaking` is a major (still a minor while on 0.x, since 1.0.0 is
// reserved for "v1 is done"), any `### Added` or `### Changed` is a minor,
// and `### Fixed` alone is a patch. Bullets under any other heading count as
// a minor, so an unsorted entry never ships as a quiet patch.
export function suggestBump(src: string, version: string): Suggestion {
  const { unreleased } = parseChangelog(src)
  if (!unreleased || !/^\s*- \S/m.test(unreleased)) return { bump: null, reason: "`## Unreleased` has nothing in it" }
  const headings = [...unreleased.matchAll(/^### (.+)$/gm)].map((m) => m[1]!.trim().toLowerCase())
  const preOne = version.startsWith("0.")
  if (headings.includes("breaking"))
    return preOne
      ? { bump: "minor", reason: "has Breaking, but 0.x stays on minors until v1 is done" }
      : { bump: "major", reason: "has Breaking" }
  const featureish = headings.filter((h) => h === "added" || h === "changed")
  if (featureish.length) return { bump: "minor", reason: `has ${[...new Set(featureish)].map((h) => h[0]!.toUpperCase() + h.slice(1)).join(" and ")}` }
  if (headings.length && headings.every((h) => h === "fixed")) return { bump: "patch", reason: "only Fixed" }
  return { bump: "minor", reason: "notes aren't sorted under Added, Changed or Fixed, so playing it safe" }
}
