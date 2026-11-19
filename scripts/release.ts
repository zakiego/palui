// Release helper. Runs on plain Node 24 (type stripping), no dependencies.
//
//   pnpm release suggest                   which bump Unreleased calls for, and the next version
//   pnpm release open <patch|minor|major|X.Y.Z> [--date YYYY-MM-DD]
//                                          on a clean checkout of origin/main: cut the release on
//                                          release/vX.Y.Z, commit and push it (the PR comes next)
//   pnpm release tag-merged                tag the commits on origin/main that set each untagged
//                                          version and push the tags (no-op when it's tagged)
//   pnpm release ship <bump> [--date]      cut, commit, push main, tag (only when the owner asks
//                                          to skip the release PR)
//   pnpm release check                     changelog and package.json agree
//   pnpm release notes [X.Y.Z]             print a release's notes (default: current)
//   pnpm release prepare <bump> [--date]   only bump and move Unreleased, no git
//   pnpm release tag                       only tag HEAD (recovery, when ship pushed main but not the tag)
//
// A nightly T3 scheduled task runs `tag-merged`, `suggest`, then `open` and opens the PR.
// Merging that PR is the owner's yes. The workflow lives in .agents/skills/palui-release/SKILL.md.
import { execFileSync } from "node:child_process"
import { readFileSync, writeFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { bumpVersion, checkChangelog, compareVersions, cutRelease, parseChangelog, suggestBump, type Bump } from "../apps/site/lib/changelog.ts"

const root = fileURLToPath(new URL("..", import.meta.url))
const PKG = `${root}package.json`
const CHANGELOG = `${root}apps/site/content/docs/changelog.mdx`

const read = (p: string) => readFileSync(p, "utf8")
const git = (...args: string[]) => execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim()
const currentVersion = () => (JSON.parse(read(PKG)) as { version: string }).version

function fail(msg: string): never {
  console.error(`release: ${msg}`)
  process.exit(1)
}

function check(): void {
  const problems = checkChangelog(read(CHANGELOG), currentVersion())
  if (problems.length) fail(`changelog problems:\n${problems.map((p) => `  - ${p}`).join("\n")}`)
  console.log(`ok: ${currentVersion()}`)
}

function suggest(): void {
  const from = currentVersion()
  const { bump, reason } = suggestBump(read(CHANGELOG), from)
  if (!bump) {
    console.log(`nothing to release: ${reason} (still ${from})`)
    return
  }
  console.log(`${bump}: ${from} -> ${bumpVersion(from, bump)} (${reason})`)
}

// The version a bump (or an exact X.Y.Z) leads to from the current one.
function nextVersion(target: string | undefined): string {
  if (!target) fail("say which bump: patch, minor, major or an exact X.Y.Z")
  const from = currentVersion()
  const to = ["patch", "minor", "major"].includes(target) ? bumpVersion(from, target as Bump) : target
  if (!/^\d+\.\d+\.\d+$/.test(to)) fail(`not a version: ${to}`)
  if (compareVersions(to, from) <= 0) fail(`${to} is not newer than ${from}`)
  return to
}

// Bumps package.json and moves Unreleased under the new version. Returns the new version.
function prepare(target: string | undefined, date: string): string {
  const from = currentVersion()
  const to = nextVersion(target)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) fail(`not a date: ${date}`)
  let next: string
  try {
    next = cutRelease(read(CHANGELOG), to, date)
  } catch (e) {
    fail((e as Error).message)
  }
  const pkg = read(PKG)
  const bumped = pkg.replace(`"version": "${from}"`, `"version": "${to}"`)
  if (bumped === pkg) fail(`couldn't find "version": "${from}" in package.json`)
  const problems = checkChangelog(next, to)
  if (problems.length) fail(`the result wouldn't pass the check:\n${problems.map((p) => `  - ${p}`).join("\n")}`)
  writeFileSync(CHANGELOG, next)
  writeFileSync(PKG, bumped)
  console.log(`${from} -> ${to} (${date})`)
  return to
}

// HEAD must be exactly origin/main with a clean tree. Works on `main` or detached
// (a worktree can't check out main while another checkout holds it).
function assertOnFreshMain(): void {
  if (git("status", "--porcelain")) fail("working tree isn't clean")
  git("fetch", "--quiet", "--tags", "origin", "main")
  if (git("rev-parse", "HEAD") !== git("rev-parse", "origin/main"))
    fail("HEAD isn't origin/main. Run `git switch --detach origin/main` (or `git switch main && git pull --ff-only`) first")
}

// Annotated tag vX.Y.Z on `sha`, with that commit's release notes as the message.
// `strict` runs today's changelog check. A release that already merged passed the rules
// of its own day, which can be older than today's, so tag-merged only needs its heading.
function createTag(sha: string, version: string, changelog: string, strict = true): string {
  const name = `v${version}`
  if (git("tag", "--list", name)) fail(`${name} already exists`)
  const problems = strict ? checkChangelog(changelog, version) : []
  if (problems.length) fail(`changelog problems at ${sha.slice(0, 7)}:\n${problems.map((p) => `  - ${p}`).join("\n")}`)
  const release = parseChangelog(changelog).releases[0]
  if (release?.version !== version) fail(`the newest changelog release at ${sha.slice(0, 7)} isn't ${version}`)
  git("tag", "--annotate", name, sha, "--message", `Palui Agent ${version} (${release.date})\n\n${release.body}`)
  return name
}

const tagHead = () => createTag(git("rev-parse", "HEAD"), currentVersion(), read(CHANGELOG))

const BRANCH_PREFIX = "release/v"

// Cuts the release on its own branch and pushes it. The PR, and merging it, come after.
function open(target: string | undefined, date: string): void {
  assertOnFreshMain()
  const branch = `${BRANCH_PREFIX}${nextVersion(target)}`
  if (git("ls-remote", "--heads", "origin", branch))
    fail(`${branch} already exists on origin. Close its PR with --delete-branch first, or merge it`)
  const version = prepare(target, date)
  git("switch", "--quiet", "--create", branch)
  git("add", PKG, CHANGELOG)
  git("commit", "--quiet", "--message", `chore(release): v${version}`)
  git("push", "--quiet", "--set-upstream", "origin", branch)
  console.log(`pushed ${branch} at ${git("rev-parse", "--short", "HEAD")}`)
}

const versionAt = (rev: string): string | undefined => {
  try {
    return (JSON.parse(git("show", `${rev}:package.json`)) as { version: string }).version
  } catch {
    return undefined
  }
}

// Every first-parent commit on origin/main that set a new package.json version (the merge or
// squash commit of a release PR), newest first, down to the newest version that has a tag.
// Returned oldest first, so a night that couldn't tag still gets its tag on a later run.
function untaggedReleases(): { version: string; sha: string }[] {
  const shas = git("log", "--first-parent", "--format=%H", "origin/main", "--", "package.json").split("\n").filter(Boolean)
  const found: { version: string; sha: string }[] = []
  const seen = new Set<string>()
  for (const sha of shas) {
    const version = versionAt(sha)
    if (!version || version === versionAt(`${sha}^1`) || seen.has(version)) continue
    seen.add(version)
    if (git("tag", "--list", `v${version}`)) break
    found.push({ version, sha })
  }
  return found.reverse()
}

// After a release PR merges: tag the commit on origin/main that set each untagged version
// and push the tags. A no-op when the current version is tagged.
function tagMerged(): void {
  git("fetch", "--quiet", "--tags", "origin", "main")
  const current = versionAt("origin/main")!
  const releases = untaggedReleases()
  if (!releases.length) {
    console.log(`v${current} already tagged`)
    return
  }
  if (releases.at(-1)!.version !== current) fail(`couldn't find the commit on origin/main that set ${current}`)
  for (const { version, sha } of releases) {
    const name = createTag(sha, version, git("show", `${sha}:apps/site/content/docs/changelog.mdx`), false)
    git("push", "--quiet", "origin", name)
    console.log(`tagged ${name} at ${sha.slice(0, 7)}`)
  }
}

function ship(target: string | undefined, date: string): void {
  assertOnFreshMain()
  const version = prepare(target, date)
  git("add", PKG, CHANGELOG)
  git("commit", "--quiet", "--message", `chore(release): v${version}`)
  try {
    git("push", "--quiet", "origin", "HEAD:main")
  } catch (e) {
    fail(
      `pushing main failed, so nothing is tagged. The release commit is only local.\n` +
        `Drop it with \`git reset --hard origin/main\`, refetch and ship again.\n${(e as Error).message}`,
    )
  }
  const name = tagHead()
  git("push", "--quiet", "origin", name)
  console.log(`shipped ${name} at ${git("rev-parse", "--short", "HEAD")}`)
}

function tag(): void {
  if (git("status", "--porcelain")) fail("working tree isn't clean")
  git("fetch", "--quiet", "--tags", "origin", "main")
  if (git("rev-parse", "HEAD") !== git("rev-parse", "origin/main")) fail("HEAD isn't origin/main")
  const name = tagHead()
  console.log(`tagged ${name} at ${git("rev-parse", "--short", "HEAD")}. Push it with: git push origin ${name}`)
}

function notes(version: string): void {
  const release = parseChangelog(read(CHANGELOG)).releases.find((r) => r.version === version)
  if (!release) fail(`no ${version} in the changelog`)
  console.log(release.body)
}

const [cmd, ...rest] = process.argv.slice(2)
const flag = (name: string) => {
  const i = rest.indexOf(name)
  return i === -1 ? undefined : rest[i + 1]
}
const positional = () => rest.find((a) => !a.startsWith("--") && a !== flag("--date"))
const today = new Date().toLocaleDateString("en-CA", { timeZone: "Asia/Makassar" })

switch (cmd) {
  case "suggest":
    suggest()
    break
  case "open":
    open(positional(), flag("--date") ?? today)
    break
  case "tag-merged":
    tagMerged()
    break
  case "ship":
    ship(positional(), flag("--date") ?? today)
    break
  case "check":
    check()
    break
  case "prepare":
    prepare(positional(), flag("--date") ?? today)
    break
  case "notes":
    notes(rest[0] ?? currentVersion())
    break
  case "tag":
    tag()
    break
  default:
    fail("usage: pnpm release <suggest|open|tag-merged|ship|check|notes|prepare|tag>")
}
