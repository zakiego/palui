// The commit a build was made from. The deploy writes build-info.json before
// `docker build` (scripts/build-info.ts, called by scripts/pre-deploy.sh), and the
// Dockerfile copies it into the image and points PALUI_BUILD_INFO at it. The
// runtime never looks at .git, which isn't in the image. A local or dev build has
// no file, and everything that reads it falls back to the version alone.
import { readFileSync } from "node:fs"
import { z } from "zod"

const buildInfoSchema = z.object({
  commit: z.string().regex(/^[0-9a-f]{7,40}$/),
  // Newest first, following first parents from `commit`. Each entry carries the
  // subjects that commit brought in: its own, or for a merge every non-merge
  // commit of the merged branch.
  history: z
    .array(z.object({ sha: z.string(), subjects: z.array(z.string()) }))
    .default([]),
})

export type BuildInfo = z.infer<typeof buildInfoSchema>

export function parseBuildInfo(raw: unknown): BuildInfo | null {
  const parsed = buildInfoSchema.safeParse(raw)
  return parsed.success ? parsed.data : null
}

// Never throws: a missing, unreadable or malformed file means "no commit known".
export function loadBuildInfo(path: string | undefined = process.env.PALUI_BUILD_INFO): BuildInfo | null {
  if (!path) return null
  try {
    return parseBuildInfo(JSON.parse(readFileSync(path, "utf8")))
  } catch {
    return null
  }
}

export function shortSha(sha: string): string {
  return sha.slice(0, 7)
}

// The subjects between `previous` (exclusive) and the build's commit, newest
// first. Unknown previous commit (too old, or none): only the head's subjects.
export function subjectsSince(info: BuildInfo, previous: string | null): string[] {
  const idx = previous ? info.history.findIndex((h) => h.sha === previous || h.sha.startsWith(previous)) : -1
  const entries = idx >= 0 ? info.history.slice(0, idx) : info.history.slice(0, 1)
  return entries.flatMap((h) => h.subjects)
}
