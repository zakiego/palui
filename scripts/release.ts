// Release helper. Runs on plain Node 24 (type stripping), no dependencies.
//
//   pnpm release check                     changelog and package.json agree
//   pnpm release prepare <patch|minor|major|X.Y.Z> [--date YYYY-MM-DD]
//                                          bump the root version, move Unreleased under it
//   pnpm release notes [X.Y.Z]             print a release's notes (default: current)
//   pnpm release tag                       tag main as vX.Y.Z (run after the release PR merged)
//
// The workflow around it lives in .agents/skills/palui-release/SKILL.md.
import { execFileSync } from "node:child_process"
import { readFileSync, writeFileSync } from "node:fs"
import { fileURLToPath } from "node:url"
import { bumpVersion, checkChangelog, compareVersions, cutRelease, parseChangelog, type Bump } from "../apps/site/lib/changelog.ts"

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

function prepare(target: string | undefined, date: string): void {
  if (!target) fail("say which bump: patch, minor, major or an exact X.Y.Z")
  const from = currentVersion()
  const to = ["patch", "minor", "major"].includes(target) ? bumpVersion(from, target as Bump) : target
  if (!/^\d+\.\d+\.\d+$/.test(to)) fail(`not a version: ${to}`)
  if (compareVersions(to, from) <= 0) fail(`${to} is not newer than ${from}`)
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
  console.log(`${from} -> ${to} (${date}). Review the changelog, then commit as "chore(release): v${to}".`)
}

function notes(version: string): void {
  const release = parseChangelog(read(CHANGELOG)).releases.find((r) => r.version === version)
  if (!release) fail(`no ${version} in the changelog`)
  console.log(release.body)
}

function tag(): void {
  const version = currentVersion()
  const name = `v${version}`
  if (git("branch", "--show-current") !== "main") fail("tag from main, after the release PR merged")
  if (git("status", "--porcelain")) fail("working tree isn't clean")
  git("fetch", "--quiet", "--tags", "origin", "main")
  if (git("rev-parse", "HEAD") !== git("rev-parse", "origin/main")) fail("local main isn't origin/main. Run `git pull --ff-only` first")
  if (git("tag", "--list", name)) fail(`${name} already exists`)
  check()
  const release = parseChangelog(read(CHANGELOG)).releases[0]!
  git("tag", "--annotate", name, "--message", `Palui Agent ${version} (${release.date})\n\n${release.body}`)
  console.log(`tagged ${name} at ${git("rev-parse", "--short", "HEAD")}. Push it with: git push origin ${name}`)
}

const [cmd, ...rest] = process.argv.slice(2)
const flag = (name: string) => {
  const i = rest.indexOf(name)
  return i === -1 ? undefined : rest[i + 1]
}
const today = new Date().toLocaleDateString("en-CA", { timeZone: "Asia/Makassar" })

switch (cmd) {
  case "check":
    check()
    break
  case "prepare":
    prepare(rest.find((a) => !a.startsWith("--") && a !== flag("--date")), flag("--date") ?? today)
    break
  case "notes":
    notes(rest[0] ?? currentVersion())
    break
  case "tag":
    tag()
    break
  default:
    fail("usage: pnpm release <check|prepare|notes|tag>")
}
