# AGENTS.md

Palui Agent: a self-hosted personal AI assistant (WhatsApp, Telegram, web chat) where channels, models, memory and tools are connectors you add and manage from the dashboard. `docs/PLAN.md` is the source of truth for scope and behavior. Read the section you're working on before writing code.

## Layout

```
apps/server          Go module: one binary, `palui serve` (HTTP, auth, /health, migrations, channels, turns, schedulers, outbox, connector supervisor) plus the admin CLI
  cmd/palui            entry point: serve, owner, connectors, backup, secrets-check, hook, healthcheck, snapshot, eval-reminders, dev
  internal/db          open, pragmas, handwritten SQL migrations (migrations/*.sql, embedded), row types (schema.go)
  internal/<domain>    chat, flows, reminders, jobs, outbound, outbox, settings, tools, models, auth, httpapi, ...
  internal/connectors  the contract (define.go), supervisor, registry, and one package per connector type
  testdata             frozen golden files (and gen/, the TS scripts that made them; see its README)
apps/app             Dashboard: React SPA (Vite, TanStack Router) under /app, built to dist/ and embedded in the Go binary
apps/host-bridge     Host-side MCP server (launchd, not Docker): an allowlist of CLI commands as tools, reached at host.docker.internal:4893
apps/site            Next 16 static export + Fumadocs (Cloudflare Pages)
apps/edge            Cloudflare Worker on palui.zakiego.com: app paths to the tunnel, the rest to Pages
```

## Contracts (don't change without updating every user)

- `apps/server/internal/connectors/define.go`: the connector contract. A connector type is a package in `internal/connectors/<type>` with a `Manifest` and a `Runtime` (`Test`, `Start`), listed in `internal/connectors/registry`. The dashboard has a TS copy of every manifest in `apps/app/src/shared/connectors` for its forms; both copies are checked against `testdata/connectors.json`, so a manifest change touches all three.
- `apps/server/internal/db/schema.go` and `internal/db/migrations`: the data model (PLAN 9). A schema change is a new numbered SQL file (never edit an applied one) plus the row type, its column list and its scan function. The server migrates at start and reports `newer` (503 on `/health`) for a migration it doesn't know.
- The dashboard API: `GET|POST /api/rpc/<name>` with the wrappers in `apps/app/src/lib/api`. Every endpoint needs a session (except `getSession`, `getInvite`, `acceptInvite`), and every write passes the same-origin check.
- Tool names exposed to the model are namespaced `<connectorId>__<tool>` (sanitized, at most 64 characters). Classes are `read`, `write`, `destructive`; anything unclassified is `destructive` (fail closed).
- Secrets are only ever stored through `connectors.Upsert` (AES-256-GCM with `MASTER_KEY`) and never returned to the UI except masked.

## Processes and data

- One process, one SQLite file (`$DATA_DIR/palui.db`, WAL, busy_timeout 5000). WhatsApp sessions live in `$DATA_DIR/wa/<connectorId>.db`.
- Inbound message: channel -> `inbound` writes `messages` (status `pending`) -> router -> per-chat queue -> turn -> `outbox` -> outbox worker -> channel `send()` -> `sent`.
- Inbound hooks: the server verifies `POST /api/hooks/:slug`, then inserts a `pending` message into the hook's chat whose `content` is `{ text, hook: { id, name, mode } }` and pokes the engine. The engine treats it as untrusted: mode `forward` sends the text as is (no model), mode `summarize` runs a turn with zero tools, tainted.
- Outbox actions use ids the router understands: `approve:<id>`, `deny:<id>`, `rem:done:<id>`, `rem:snooze:<id>:<10m|1h|tomorrow>`, `connector:<on|off>:<connectorId>`, `fl:<runId>:<answerId>` (flow check-in answers), `flowver:<approve|deny>:<versionId>` (flow version approvals). A flow run's per-run approval reuses `approve:`/`deny:` with a `flr_` run id.
- Flows (PLAN 6c): `internal/flows` is the engine (a scheduler extension), `internal/connectors/flows` the agent tools. Hooks in mode `flow` start flow runs instead of writing a message; `chat` triggers are matched by the router before the model.
- Delivery is at least once. Never delete a reminder or outbox row before the channel acks.

## Conventions

- Go: the version in `apps/server/go.mod`, `gofmt`, standard library first, no cgo (`modernc.org/sqlite`). Node 24 and pnpm 12 for the TS workspace (TypeScript 7, `tsc` is tsgo, ESM).
- Database: every query helper takes `db.Q`. Inside `Tx(func(tx db.Q) error)` use only `tx`: calling the outer `*db.DB` deadlocks on the single write connection. Claims are one `UPDATE ... RETURNING`. Always name columns.
- Clock: functions take `now int64` (Unix ms) as the last parameter and never read the clock themselves; tests pass fixed times or `testutil.FakeClock`.
- JSON: write through `jsonx` (`jsonx.Marshal`, `jsonx.Object` keeps key order like JS), because stored JSON, prompts and hashes must keep their exact bytes. User-visible and stored length limits count UTF-16 units (`strx.Len16`, `strx.Slice16`).
- Go comments that name a TS file (`packages/core/src/...`, `apps/runtime/...`, `apps/app/src/server/...`) or Baileys point at the TypeScript backend the code was ported from, at commit `2c89fa5`: `git show 2c89fa5:<path>` reads it.
- Tests: Go tests next to the code (`*_test.go`); `testutil.DB(t)` is a migrated in-memory database. Goldens in `apps/server/testdata` are frozen: a deliberate behavior change edits the golden in the same change. Dashboard tests are vitest; component tests are `*.test.tsx` with `// @vitest-environment jsdom` at the top.
- Dashboard UI is ui-sdk (`@ui-sdk/react`, `@ui-sdk/core`), wrapped and styled in `apps/app/src/components/ui`. Before touching it, follow the `ui-sdk` skill if it's installed and read `apps/app/node_modules/@ui-sdk/react/docs/llms.txt`. Don't add shadcn, Base UI or Radix.
- English only in code, comments, commits and docs. Bot-facing copy (chat replies, Indonesian error texts the model or owner sees) is casual Indonesian. No em dashes anywhere.
- Conventional Commits, one logical change per commit, tests passing at every commit.
- The shell hook blocks any Bash command whose text contains `.env` (that includes `process.env`). Write such files with the Write/Edit tools, not heredocs.
- Dev ports: dashboard (Vite) 4890, Go server 3000, site 4892. Prod compose binds the server to 127.0.0.1:4802.

## Commands

```bash
pnpm install
pnpm dev:server          # Go server on :3000 (data in apps/server/data)
pnpm dev                 # dashboard on :4890, proxying /api and /health to :3000
pnpm cli owner invite    # single-use owner link (15 min); any `palui` command works
pnpm test && pnpm typecheck && pnpm build   # workspace packages, then apps/server (scripts/go.sh)
pnpm eval:reminders      # 60-phrase parse eval, live against CLIProxyAPI (needs PALUI_EVAL_KEY)
pnpm release check       # package.json version and changelog agree
pnpm release suggest     # the bump Unreleased calls for
pnpm release open minor  # cut the release on release/vX.Y.Z and push it (nightly task opens the PR)
pnpm release tag-merged  # tag the merged release on main
bash scripts/setup-worktree.sh   # new worktree: copy local env files, install (T3 runs it on create)
bash scripts/clean-worktree.sh   # settled worktree: drop node_modules and build output (T3 runs it on settle)
```

From `apps/server`, the Go gate is `gofmt -l .` (prints nothing), `go build ./...`, `go vet ./...`, `go test ./...`.

`t3.json` declares these T3 Code actions. T3 doesn't run them from the file alone: import them in Settings > Projects > Actions.

## Versions and changelog

- SemVer. The version lives only in the root `package.json` `"version"`. The Docker build stamps it into the binary (`internal/version`), and `GET /health` reports it.
- The changelog is `apps/site/content/docs/changelog.mdx` (https://palui.zakiego.com/docs/changelog). Every user-visible PR adds its bullet under `## Unreleased` in the same PR. `apps/site/lib/changelog.test.ts` fails when the changelog and the version disagree.
- The owner doesn't pick versions. A T3 scheduled task runs `palui-release` every night at 00:00 WITA in a new thread: it tags the last merged release (`pnpm release tag-merged`), then opens a `chore(release): vX.Y.Z` PR from `release/vX.Y.Z` when Unreleased has something (`pnpm release open <bump>`). Merging that PR is the owner's yes. Other PRs never bump the version. Never bump it by hand. While on `0.x`, the v1 scope is still being built; `1.0.0` means v1 is done, and only the owner calls that.

## Skills

Repo workflows live in `.agents/skills/` (`.claude/skills` links there). Use them instead of improvising:

- `palui-changelog`: add an Unreleased entry for a user-visible change.
- `palui-release`: the nightly release run: tag the merged release, open the next release PR.
- `palui-pr`: checks and conventions before opening a PR against `main`.
- `palui-new-connector`: add a connector end to end.
- `palui-schema-change`: add a migration and ship it safely.
- `palui-ui-check`: check a dashboard change against the fixtures.

## Fixture data (dev only)

`apps/server/internal/fixtures` holds five datasets for the dashboard: `demo`, `worst` (the realistic worst case for every rendered value), `empty`, `one` and `huge`. Fill a scratch database with one, then run the dashboard against it:

```bash
cd apps/server
DATA_DIR=/tmp/palui-demo go run ./cmd/palui dev seed worst
DATA_DIR=/tmp/palui-demo go run ./cmd/palui dev serve --host 127.0.0.1 --port 3000   # stand-in connectors, no network
pnpm dev                 # from the repo root, in another terminal
```

On a database a fixture filled, the dev dashboard shows a "Demo data / Worst case / Empty / One / 1,000 rows" toggle at the bottom. It replaces every product row, so it refuses a database with data that didn't come from a fixture, and it isn't in a production build. Check a UI change against `worst` at 390 px and 768 px before calling it done. When a new field reaches the UI, add its worst case to `internal/fixtures/worst.go`.
