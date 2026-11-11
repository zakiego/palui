---
name: palui-release
description: Release Palui Agent. The agent picks the semver bump from the changelog, asks the owner once, then cuts, commits, pushes and tags the release on main from the local machine. Use right after a user-visible palui PR merges, before a deploy, or when asked to "release", "cut a release", "bump the version", "ship 0.2.0", "tag it" or "what version are we on".
---

# Palui release

The owner doesn't think about versions. You pick the bump, ask once, and do everything else locally. No release PR, no CI.

The version lives only in `"version"` in the root `package.json`. `packages/core/src/version.ts` reads it, and `GET /health` reports it. The changelog is `apps/site/content/docs/changelog.mdx`. `apps/site/lib/changelog.test.ts` fails `pnpm test` when the two disagree. All mechanics go through `pnpm release` (`scripts/release.ts`). Never edit the version or move changelog sections by hand.

## When to run it

- Right after a PR that added a changelog line merges. If you merged it, or the owner says it merged, offer the release in the same reply.
- Before a deploy, if `## Unreleased` on origin/main has anything in it.
- When the owner asks.

PRs never bump the version themselves, so parallel PRs can't fight over a number.

## Steps

1. Get on a clean, fresh origin/main. In a worktree main is usually checked out elsewhere, so detach:
   ```bash
   git fetch origin && git switch --detach origin/main
   ```
   With uncommitted work in the checkout, use a fresh worktree instead of touching it.
2. Ask the script what the notes call for:
   ```bash
   pnpm release suggest
   ```
   It prints e.g. `minor: 0.1.0 -> 0.2.0 (has Added)` or `nothing to release`. If nothing, stop and say so.
3. Sanity-check the suggestion against the notes yourself. The rules:

   | Unreleased has | 0.x | >= 1.0 |
   | --- | --- | --- |
   | only `### Fixed` | patch | patch |
   | any `### Added` or `### Changed` | minor | minor |
   | any `### Breaking` | minor | major |

   `1.0.0` happens only when the owner says v1 is done. Never suggest it on your own; you can mention it when the v1 scope in `docs/PLAN.md` looks finished. If a bullet sits under the wrong heading (a feature filed under Fixed), fix the heading on a branch first rather than overriding the bump.
4. Ask the owner with AskUserQuestion, one question. Put the suggestion first and marked "(Recommended)", the other sensible bumps as options, and the Unreleased bullets in the question so they see what ships. Keep it short, e.g. "Release 0.2.0? It has 2 Added, 1 Fixed."
5. On a yes, ship it. This bumps, commits `chore(release): vX.Y.Z`, pushes main, tags and pushes the tag:
   ```bash
   pnpm release ship minor        # or patch, major, or an exact 0.4.0
   ```
   The date defaults to today in Asia/Makassar; pass `--date YYYY-MM-DD` only to backdate.
6. Report the result in one line: version, short sha, and the changelog link https://palui.zakiego.com/docs/changelog (the site redeploys from main).

The owner's yes on step 4 covers the commit and both pushes for that release. It doesn't cover a GitHub Release or a deploy: those are separate asks.

## When it goes wrong

- `HEAD isn't origin/main`: main moved or you're on a branch. Redo step 1.
- `pushing main failed`: someone pushed in between. Nothing is tagged. `git reset --hard origin/main` (only the local release commit is lost), then start over at step 1.
- Main pushed but the tag push failed: from that same commit, `pnpm release tag` then `git push origin vX.Y.Z`.
- Never reuse, move or delete a tag, and never force-push. A broken release gets a new patch.

## Rules

- Never put a release heading above `## Unreleased`, and never edit an old release's notes except to fix a typo.
- Heading shape is exact: `## 0.2.0 (2026-11-20)`. The test rejects `## v0.2.0`, `## 0.2.0 - 2026-11-20` and friends.
- Check what's running after a deploy:
  ```bash
  curl -s https://palui.zakiego.com/health | jq '{version, runtime: .runtime.version}'
  ```
