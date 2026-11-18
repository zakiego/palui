---
name: palui-release
description: Release Palui Agent. A T3 scheduled task runs this every night at 00:00 WITA in a fresh thread. It tags the last merged release, picks the semver bump from the changelog, and opens a release PR. Merging that PR is the owner's yes. Use when the nightly scheduled run fires, or when asked to "release", "cut a release", "bump the version", "ship 0.2.0", "tag it" or "what version are we on".
---

# Palui release

The owner doesn't think about versions. Every night a T3 scheduled task ("Palui nightly release", 00:00 Asia/Makassar, new thread per run) runs the steps below. It opens a release PR when `## Unreleased` has something in it. The owner merges that PR when they want the release out, and the next nightly run tags it. No CI, no GitHub Release.

The version lives only in `"version"` in the root `package.json`. The Docker build stamps it into the Go binary (`apps/server/internal/version`), and `GET /health` reports it. The changelog is `apps/site/content/docs/changelog.mdx`. `apps/site/lib/changelog.test.ts` fails `pnpm test` when the two disagree. All mechanics go through `scripts/release.ts` (`pnpm release <cmd>`, or `node scripts/release.ts <cmd>` when node_modules isn't installed; it has no dependencies). Never edit the version or move changelog sections by hand.

PRs other than the release PR never bump the version, so parallel PRs can't fight over a number.

## Nightly steps

1. Work in a throwaway worktree on a fresh origin/main, never the owner's checkout:
   ```bash
   git fetch origin --tags
   git worktree add --detach /tmp/palui-release-$(date +%Y%m%d) origin/main
   cd /tmp/palui-release-$(date +%Y%m%d)
   ```
2. Tag a release PR that merged since last night. It finds the first-parent commit on main that set the current version (merge or squash commit) and pushes `vX.Y.Z`. It's a no-op when the tag already exists:
   ```bash
   node scripts/release.ts tag-merged
   ```
3. Ask what the notes call for:
   ```bash
   node scripts/release.ts suggest
   ```
   `nothing to release`: go to step 7.
4. Sanity-check the suggestion against the notes:

   | Unreleased has | 0.x | >= 1.0 |
   | --- | --- | --- |
   | only `Fixed:` entries | patch | patch |
   | any `Added:` or `Changed:` entry | minor | minor |
   | any `### Breaking` | minor | major |

   `1.0.0` happens only when the owner says v1 is done. Never open it on your own. If an entry has the wrong type (a feature marked `Fixed:`), go with the script's bump and say so in the PR body. Don't fix entries in the nightly run.
5. Check for an open release PR:
   ```bash
   gh pr list --state open --json number,headRefName,url -q '.[] | select(.headRefName | startswith("release/v"))'
   git fetch origin <headRefName> && git rev-parse FETCH_HEAD^1 origin/main
   ```
   - The release commit's parent is still origin/main (both shas match) and its version matches the suggestion: nothing changed, keep it. Go to step 7.
   - Otherwise it's stale. Close it without a comment: `gh pr close <n> --delete-branch`.
6. Cut the release on `release/vX.Y.Z`, push it, open the PR:
   ```bash
   node scripts/release.ts open minor        # or patch, major, or an exact 0.9.0
   node scripts/release.ts notes             # the bullets for the body
   gh pr create --draft --base main --assignee zakiego --label enhancement \
     --title "chore(release): vX.Y.Z" --body-file /tmp/palui-release-pr.md
   ```
   Use label `bug` when the release is only `### Fixed`. Body (unwrapped lines):
   ```md
   TL;DR: Release X.Y.Z (<bump>, <counts, e.g. 2 Added, 1 Fixed>). Merge it to ship; the next nightly run tags vX.Y.Z.

   ## What ships
   <output of `node scripts/release.ts notes`>

   ## How I checked
   - `pnpm release check` passes on the branch
   ```
   The date in the heading defaults to today in Asia/Makassar. Link the PR to the thread (`link_pull_request`).
7. Clean up and report in one line: what got tagged, the PR link, or "nothing to release".
   ```bash
   cd - && git worktree remove /tmp/palui-release-$(date +%Y%m%d)
   ```

The owner opted into this schedule, which covers pushing `release/v*` branches, opening and closing release PRs, and pushing `vX.Y.Z` tags. It doesn't cover merging the PR, a GitHub Release, or any other post.

## Releasing by hand

When the owner asks for a release right now, run steps 1 to 6 yourself. Only when they explicitly say to skip the PR, `node scripts/release.ts ship <bump>` commits, pushes main and tags in one go.

## When it goes wrong

- `HEAD isn't origin/main`: main moved or you're on a branch. Redo step 1.
- `entry doesn't end with its PR link`: an Unreleased bullet has no PR link. Find its PR with `git log origin/main -S '<a phrase from the bullet>' -- apps/site/content/docs/changelog.mdx`, open a `docs(changelog): link missing PRs` PR that adds the links, and report it. Don't cut the release that night.
- `release/vX.Y.Z already exists on origin`: a PR from an earlier run is still open. Step 5 decides whether to keep or close it.
- `couldn't find the commit on origin/main that set X.Y.Z`: someone bumped the version outside a release PR. Stop and tell the owner.
- The tag push failed after tagging: `git push origin vX.Y.Z`.
- Never reuse, move or delete a tag, and never force-push. A broken release gets a new patch.

## Rules

- Never put a release heading above `## Unreleased`, and never edit an old release's notes except to fix a typo.
- Heading shape is exact: `## 0.2.0 (2026-11-20)`. The test rejects `## v0.2.0`, `## 0.2.0 - 2026-11-20` and friends.
- Merging to main redeploys. Check what's running:
  ```bash
  curl -s https://palui.zakiego.com/health | jq '{version, runtime}'
  ```
