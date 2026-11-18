// Parses and checks content/docs/changelog.mdx, the one changelog. The site test
// (changelog.test.ts) runs the check on every `pnpm test`, and scripts/release.ts
// uses the same code to cut a release, so this file imports nothing.
//
// Shape it expects:
//   ## Unreleased                 always first, "Nothing yet." when empty
//   ## 0.2.0 (2026-11-20)         one per release, newest first
//   ## Not in v1                  any other h2 that isn't version-like is free text
//
// Inside Unreleased and each release:
//   ### Breaking                  first, when the release needs action on upgrade
//   ### Chat                      then the feature categories, in CATEGORIES order
//   - Added: Something new.       every entry under a category starts with its type

export type Release = { version: string; date: string; body: string }
export type Changelog = { unreleased: string | null; releases: Release[] }

export const EMPTY_UNRELEASED = "Nothing yet."

// The h3 groups, in the order they appear. Breaking isn't a feature: it's the
// list of things to do on upgrade, so it always comes first.
export const CATEGORIES = ["Chat", "Agents", "Tools", "Automations", "Dashboard", "API", "Self-hosting", "Site"] as const
export const CHANGE_TYPES = ["Added", "Changed", "Fixed"] as const
export type ChangeType = (typeof CHANGE_TYPES)[number]
const HEADINGS = ["Breaking", ...CATEGORIES] as string[]
const TYPED_ENTRY = new RegExp(`^- (${CHANGE_TYPES.join("|")}): \\S`)

const RELEASE_HEADING = /^## (\d+\.\d+\.\d+) \((\d{4}-\d{2}-\d{2})\)$/
// Anything that starts like a version but isn't the exact shape above.
const VERSION_LIKE = /^## v?\d/
const SEMVER = /^(\d+)\.(\d+)\.(\d+)$/
// Every released entry ends with the PR (or PRs) that made it:
// ([#123](https://github.com/zakiego/palui/pull/123)), comma-separated for several.
export const PR_URL = "https://github.com/zakiego/palui/pull/"
const PR_REF = String.raw`\[#(\d+)\]\(https://github\.com/zakiego/palui/pull/\1\)`
const PR_LINK_END = new RegExp(String.raw`\(${PR_REF}(?:, \[#(\d+)\]\(https://github\.com/zakiego/palui/pull/\2\))*\)$`)

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

// The h3 and entry rules for one section (Unreleased or a release).
function checkGroups(s: Section, label: string): string[] {
  const problems: string[] = []
  let heading: string | null = null
  let last = -1
  let fenced = false
  for (const [j, l] of s.body.entries()) {
    const at = `line ${s.line + 1 + j}`
    if (l.startsWith("```")) fenced = !fenced
    if (fenced) continue
    if (l.startsWith("### ")) {
      heading = l.slice(4).trim()
      const rank = HEADINGS.indexOf(heading)
      if (rank === -1) problems.push(`${at}: "${l.trimEnd()}" in ${label} isn't one of ${HEADINGS.join(", ")}`)
      else if (rank <= last) problems.push(`${at}: "${heading}" in ${label} is out of order or repeated, keep ${HEADINGS.join(", ")}`)
      else last = rank
    } else if (l.startsWith("- ")) {
      if (heading === null) problems.push(`${at}: ${label} entry isn't under a category like \`### Chat\``)
      else if (heading !== "Breaking" && !TYPED_ENTRY.test(l))
        problems.push(`${at}: ${label} entry should start with ${CHANGE_TYPES.map((t) => `"${t}: "`).join(", ")}`)
    }
  }
  return problems
}

// Every rule the changelog must hold, as readable problems. Empty means fine.
// `version` is the root package.json version.
export function checkChangelog(src: string, version: string): string[] {
  const problems: string[] = []
  const all = sections(src)
  const unreleasedAt = all.findIndex((s) => s.heading === "## Unreleased")
  if (unreleasedAt === -1) problems.push("missing the `## Unreleased` section")
  if (all.filter((s) => s.heading === "## Unreleased").length > 1) problems.push("more than one `## Unreleased` section")
  if (unreleasedAt !== -1) problems.push(...checkGroups(all[unreleasedAt]!, "Unreleased"))

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
    problems.push(...checkGroups(s, v))
    for (const [j, l] of s.body.entries())
      if (l.startsWith("- ") && !PR_LINK_END.test(l.trimEnd()))
        problems.push(`line ${s.line + 1 + j}: ${v} entry doesn't end with its PR link, like ([#123](${PR_URL}123))`)
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
// reserved for "v1 is done"), any `Added:` or `Changed:` entry is a minor,
// and `Fixed:` alone is a patch. An entry without a type counts as a minor,
// so an unsorted entry never ships as a quiet patch. Older notes that sort
// entries under `### Added`, `### Changed` and `### Fixed` still read the same.
export function suggestBump(src: string, version: string): Suggestion {
  const { unreleased } = parseChangelog(src)
  if (!unreleased || !/^\s*- \S/m.test(unreleased)) return { bump: null, reason: "`## Unreleased` has nothing in it" }
  const types: string[] = []
  let heading = ""
  for (const l of unreleased.split("\n")) {
    if (l.startsWith("### ")) heading = l.slice(4).trim().toLowerCase()
    else if (l.startsWith("- ")) {
      const typed = /^- (Added|Changed|Fixed): /.exec(l)
      types.push(heading === "breaking" ? "breaking" : typed ? typed[1]!.toLowerCase() : ["added", "changed", "fixed"].includes(heading) ? heading : "unsorted")
    }
  }
  const preOne = version.startsWith("0.")
  if (types.includes("breaking"))
    return preOne
      ? { bump: "minor", reason: "has Breaking, but 0.x stays on minors until v1 is done" }
      : { bump: "major", reason: "has Breaking" }
  const featureish = types.filter((t) => t === "added" || t === "changed")
  if (featureish.length) return { bump: "minor", reason: `has ${[...new Set(featureish)].map((t) => t[0]!.toUpperCase() + t.slice(1)).join(" and ")}` }
  if (types.every((t) => t === "fixed")) return { bump: "patch", reason: "only Fixed" }
  return { bump: "minor", reason: "some entries don't say Added, Changed or Fixed, so playing it safe" }
}
