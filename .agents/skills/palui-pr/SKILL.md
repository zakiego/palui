---
name: palui-pr
description: Open a pull request in zakiego/palui the repo's way. Branches from origin/main, runs the checks for the packages touched, makes sure the changelog has the change, and opens a draft PR against main with a TL;DR-first body. Use when asked to "open a PR", "create a PR", "ship this", or "/pd" in the palui repo. Overrides the generic github-create-pr skill here.
---

# Palui PR

## Before pushing

1. The branch is based on `origin/main`, and the PR targets `main`. Never `feat/v1` or another feature branch.
2. Commits are Conventional Commits, one logical change each, tests passing at every commit. Scopes in use: `server` (the Go server, or a narrower area like `db`, `auth`, `flows`), `app` (the dashboard), `site`, `edge`, `host-bridge`, `connectors`, or the connector name (`whatsapp`, `telegram`...). Breaking changes get `!`.
3. Run the checks for every package the diff touches (find them with `git diff --name-only origin/main...`):
   ```bash
   (cd apps/server && gofmt -l . && go vet ./... && go test ./...)   # Go; gofmt must print nothing
   (cd <package> && npx tsc --noEmit && npx vitest run)               # each TS package
   ```
   For `apps/site`, typecheck is `pnpm typecheck` (it runs `next typegen` first). The root `pnpm typecheck`, `pnpm test` and `pnpm build` run every package and then the Go module, so they're the full gate. Run `pnpm build` when the change touches build config, the Dockerfile or bundling.
4. Changelog: if the change is user-visible, `## Unreleased` in `apps/site/content/docs/changelog.mdx` has its bullet (`palui-changelog`). `pnpm release check` passes. Don't bump the version in the PR: that happens in the nightly release PR (`palui-release`).
5. Schema changed? The new `apps/server/internal/db/migrations/NNNN_*.sql` file is numbered after main's last one and `schema.go` matches it (`palui-schema-change`).
6. Visible dashboard change? It passed `palui-ui-check`, and you have before and after screenshots.
7. No em dashes in code, comments, docs or the PR. English everywhere except bot-facing copy, which is casual Indonesian.

## Open it

- Draft, assigned to `zakiego`, labels picked from `gh label list`.
- Body through `--body-file`, never inline. No session links.
- Title is the main commit's subject, Conventional Commits style.

```bash
gh pr create --draft --base main --assignee zakiego --label <label> --title "<type(scope): subject>" --body-file /tmp/palui-pr.md
```

Body shape (unwrapped lines, GitHub reflows them):

```md
TL;DR: <one or two sentences on what changes for the owner running Palui>

## What changed
- <bullet per logical change>

## How I checked
- <commands run and their result, e.g. "apps/app: 214 tests pass">
- <screenshots for visible changes>

## Changelog
<the Unreleased bullets this PR adds, or "None: internal only">
```

When the user asked for the PR, push and create it without showing a draft first, then send the link. Otherwise show the title and body and wait.

If the PR adds a changelog line, end the reply with the release it will become, e.g. "After merge this goes into the 0.2.0 (minor) release PR the nightly run opens at 00:00 WITA."
