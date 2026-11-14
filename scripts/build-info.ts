// Writes build-info.json at the repo root: the commit being built and the
// subjects of the commits before it. scripts/pre-deploy.sh runs it after the
// pull, the Dockerfile copies the file into the image, and the server reads it
// on start to tell the owner about a new commit (apps/server/internal/version).
//
//   node scripts/build-info.ts          # writes ./build-info.json
//   node scripts/build-info.ts --print  # prints it instead
import { execFileSync } from "node:child_process"
import { writeFileSync } from "node:fs"
import { join } from "node:path"

// How far back the runtime can list subjects. Further back it shows only the newest.
const HISTORY = 50

const root = join(import.meta.dirname, "..")
const git = (...args: string[]) => execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim()
const lines = (s: string) => s.split("\n").filter(Boolean)

const commit = git("rev-parse", "HEAD")
// Walk first parents (main's own line). A merge brings in every non-merge commit
// of the merged branch; any other commit brings in itself.
const history = lines(git("log", "--first-parent", `-n${HISTORY}`, "--format=%H %P")).map((line) => {
  const [sha, first, ...rest] = line.split(" ")
  const subjects = rest.length > 0 && first ? lines(git("log", "--no-merges", "--format=%s", `${first}..${sha}`)) : [git("log", "-1", "--format=%s", sha!)]
  return { sha: sha!, subjects }
})

const json = `${JSON.stringify({ commit, history }, null, 2)}\n`
if (process.argv.includes("--print")) process.stdout.write(json)
else writeFileSync(join(root, "build-info.json"), json)
