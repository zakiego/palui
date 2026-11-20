# Contributing

Thanks for looking. Palui is a personal project with one maintainer, so small, focused pull requests get reviewed fastest. For anything bigger than a bug fix, open an issue first so we can agree on the approach.

## Setup

You need Go (the version in `apps/server/go.mod`), Node 24 and pnpm (the version in `package.json` `packageManager`).

```bash
pnpm install
pnpm dev:server   # Go server on :3000, data in apps/server/data
pnpm dev          # dashboard on :4890, proxies /api and /health to :3000
pnpm cli owner invite   # sign-in link for your local owner
```

To work on the dashboard without real connectors, fill a scratch database with fixture data:

```bash
cd apps/server
DATA_DIR=/tmp/palui-demo go run ./cmd/palui dev seed demo
DATA_DIR=/tmp/palui-demo go run ./cmd/palui dev serve --host 127.0.0.1 --port 3000
```

## Before you open a PR

```bash
pnpm test && pnpm typecheck && pnpm build
```

That runs the TypeScript workspace and then the Go gate (`gofmt`, `go vet`, `go test`, `go build`). CI runs the same checks.

- One logical change per commit, with [Conventional Commits](https://www.conventionalcommits.org) messages (`fix(reminders): keep the snooze time zone`).
- A user-visible change adds one short bullet under `## Unreleased` in `apps/site/content/docs/changelog.mdx`.
- A schema change is a new numbered SQL file in `apps/server/internal/db/migrations`. Never edit one that already shipped.
- Golden files in `apps/server/testdata` are frozen. A deliberate behavior change updates the golden in the same commit.
- Use English for code, comments and docs. Copy the bot sends to the owner is casual Indonesian.
- Don't put real names, phone numbers or tokens in tests, fixtures or docs. Use obvious placeholders.

`AGENTS.md` has the full set of conventions (database helpers, clock handling, JSON encoding) and the contracts that must stay in sync.

## License

By contributing, you agree your work is released under the [MIT License](LICENSE).
