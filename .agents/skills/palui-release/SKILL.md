---
name: palui-release
description: Cut a Palui Agent release. Picks the semver bump, moves the changelog's Unreleased notes under the new version, bumps the root package.json, opens the release PR and tags main as vX.Y.Z after it merges. Use when asked to "release", "cut a release", "bump the version", "tag v0.x", "ship 0.2.0", or "what version are we on".
---

# Palui release

The version lives in one place: `"version"` in the root `package.json`. Nothing else holds a copy. `packages/core/src/version.ts` reads it, and `GET /health` reports it (plus the runtime's, under `runtime.version`).

The changelog lives in one place: `apps/site/content/docs/changelog.mdx`, published at https://palui.zakiego.com/docs/changelog. `apps/site/lib/changelog.test.ts` fails `pnpm test` when the two disagree, so a bump by hand without a changelog entry can't merge quietly.

All mechanics go through `pnpm release` (`scripts/release.ts`). Don't edit the version or move changelog sections by hand.

## Pick the bump

Read `## Unreleased` and decide from what's in it, not from how much code changed.

| Version now | Unreleased has | Bump |
| --- | --- | --- |
| `0.x` | only fixes | `patch` |
| `0.x` | any feature, behavior change or breaking change | `minor` |
| `0.x` | the last piece of the v1 scope, and the owner says v1 is done | `major` (to `1.0.0`) |
| `>= 1.0` | only fixes | `patch` |
| `>= 1.0` | features, nothing the self-hoster must act on | `minor` |
| `>= 1.0` | anything under `### Breaking` | `major` |

"Breaking" means a self-hoster has to do something on upgrade: a new required env var, a compose change, a manual data step, a removed chat command or API route, a rollback that now needs a restore. A new migration alone isn't breaking (the app runs it), but it does make rollback need the pre-deploy snapshot, so say that in the entry.

If `Unreleased` says "Nothing yet.", there's nothing to release. Stop and say so.

## Steps

1. Start from fresh main on a new branch:
   ```bash
   git fetch origin && git switch -c release/vX.Y.Z origin/main
   ```
2. Cut it. The date defaults to today in Asia/Makassar; pass `--date` only to backdate.
   ```bash
   pnpm release prepare minor        # or patch, major, or an exact 0.4.0
   ```
3. Read the new section in `changelog.mdx` and tidy it: group bullets under `### Breaking` (first), `### Added`, `### Changed`, `### Fixed`, or topic headings when the release is big. Each bullet says what changed for the person running Palui, not which file moved. Breaking bullets say exactly what to do on upgrade.
4. Check:
   ```bash
   pnpm release check && (cd apps/site && npx vitest run lib/changelog.test.ts)
   ```
5. Commit `package.json` and `changelog.mdx` as one commit: `chore(release): vX.Y.Z`. Open the PR with the `palui-pr` skill, title `chore(release): vX.Y.Z`, body = the release notes (`pnpm release notes`).
6. After the PR merges, tag main from a clean checkout of it:
   ```bash
   git switch main && git pull --ff-only && pnpm release tag
   git push origin vX.Y.Z
   ```
   Pushing the tag is part of the release the user asked for. A GitHub Release (`node scripts/release.ts notes > /tmp/palui-notes.md && gh release create vX.Y.Z --notes-file /tmp/palui-notes.md`) is a separate outward post: only do it when the user asks for it.
7. Deploying is separate (`zaki-selfhost` / `deploy.sh`). After a deploy, confirm the running version:
   ```bash
   curl -s https://palui.zakiego.com/health | jq '{version, runtime: .runtime.version}'
   ```

## Rules

- Never reuse or move a tag. A broken release gets a new patch.
- Never put a release heading above `## Unreleased`, and never edit an old release's notes except to fix a typo.
- Docs-only or site-only changes still go through Unreleased if a reader would care, but they don't need their own release.
- Heading shape is exact: `## 0.2.0 (2026-11-20)`. The test rejects `## v0.2.0`, `## 0.2.0 - 2026-11-20` and friends.
