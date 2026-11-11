# AGENTS.md

Palui Agent: a self-hosted personal AI assistant (WhatsApp, Telegram, web chat) where channels, models, memory and tools are connectors you add and manage from the dashboard. `docs/PLAN.md` is the source of truth for scope and behavior. Read the section you're working on before writing code.

## Layout

```
apps/app             TanStack Start (Nitro): dashboard under /app, REST under /api, Better Auth, /health, migrations, admin CLI
apps/runtime         Plain Node process: channel sockets, turns, scheduler, outbox, connector supervisor, control API (:4891)
apps/site            Next 16 static export + Fumadocs (Cloudflare Pages)
apps/edge            Cloudflare Worker on palui.zakiego.com: app paths to the tunnel, the rest to Pages
packages/db          Drizzle schema, migrations (bundled into src/migrations.gen.ts), openDb, crypto (AES-256-GCM), ids
packages/core        Domain logic shared by app and runtime: config, log, events, settings, connectors, chat, models, tools, reminders, jobs, outbox, approvals
packages/connectors  One folder per connector: <type>/manifest.ts (zod + UI metadata) and <type>/runtime.ts (test/start)
```

## Contracts (don't change without updating every user)

- `packages/core/src/connectors/define.ts`: the connector contract. `manifest.ts` imports only `zod` and this file, because the app bundles every manifest. `runtime.ts` is imported only by the runtime.
- `packages/core/src/control.ts`: the app to runtime control API. The runtime serves it on the compose network only, with the token from `getControlToken()` in the `x-palui-control` header.
- `packages/db/src/schema.ts`: the data model (PLAN 9). After changing it run `pnpm --filter @palui/db db:generate`, which writes a new SQL file in `packages/db/drizzle/` and regenerates `src/migrations.gen.ts`. Only the app runs migrations; the runtime refuses to start on a schema it doesn't know.
- Tool names exposed to the model are namespaced `<connectorId>__<tool>`. Classes are `read`, `write`, `destructive`; anything unclassified is `destructive` (fail closed).
- Secrets are only ever stored through `upsertConnector` (encrypted with `MASTER_KEY`) and never returned to the UI except masked.

## Processes and data

- One SQLite file (`$DATA_DIR/palui.db`, WAL, busy_timeout 5000) shared by app and runtime through the same volume.
- Inbound message: channel -> `services.inbound()` writes `messages` (status `pending`) -> router -> per-chat queue -> turn -> `outbox` -> outbox worker -> channel `send()` -> `sent`.
- Inbound hooks: the app verifies `POST /api/hooks/:slug`, then inserts a `pending` message into the hook's chat whose `content` is `{ text, hook: { id, name, mode } }` and pokes the runtime. The engine treats it as untrusted: mode `forward` sends the text as is (no model), mode `summarize` runs a turn with zero tools, tainted.
- Outbox actions use ids the router understands: `approve:<id>`, `deny:<id>`, `rem:done:<id>`, `rem:snooze:<id>:<10m|1h|tomorrow>`, `connector:<on|off>:<connectorId>`.
- Delivery is at least once. Never delete a reminder or outbox row before the channel acks.

## Conventions

- Node 24, pnpm 12, TypeScript 7 (`tsc` is tsgo), ESM, `moduleResolution: bundler`, workspace packages are consumed as TS source (no build step) except the two apps.
- Tests: vitest, `src/**/*.test.ts` next to the code. Use `memoryDb()` from `@palui/db` for a migrated in-memory DB. Fake clocks: pass `now` explicitly; functions take `now = Date.now()` as a last parameter.
- Run checks per package with `npx tsc --noEmit` and `npx vitest run` (pnpm 12 has no `-s`). Root: `pnpm typecheck`, `pnpm test`, `pnpm build`.
- English only in code, comments, commits and docs. Bot-facing copy is casual Indonesian. No em dashes anywhere.
- Conventional Commits, one logical change per commit, tests passing at every commit.
- The shell hook blocks any Bash command whose text contains `.env` (that includes `process.env`). Write such files with the Write/Edit tools, not heredocs.
- Dev ports: app 4890, runtime control 4891, site 4892. Prod compose binds the app to 127.0.0.1:4802.

## Commands

```bash
pnpm install
pnpm dev                 # dashboard on :4890
pnpm dev:runtime         # runtime on :4891
pnpm cli owner invite    # single-use owner link (15 min)
pnpm test && pnpm typecheck && pnpm build
pnpm eval:reminders      # 60-phrase parse eval, live against CLIProxyAPI (needs PALUI_EVAL_KEY)
pnpm release check       # package.json version and changelog agree
pnpm release suggest     # the bump Unreleased calls for
pnpm release ship minor  # cut, commit, push main, tag (after the owner says yes)
```

## Versions and changelog

- SemVer. The version lives only in the root `package.json` `"version"`. `packages/core/src/version.ts` reads it, and `GET /health` reports it for the app and the runtime.
- The changelog is `apps/site/content/docs/changelog.mdx` (https://palui.zakiego.com/docs/changelog). Every user-visible PR adds its bullet under `## Unreleased` in the same PR. `apps/site/lib/changelog.test.ts` fails when the changelog and the version disagree.
- The owner doesn't pick versions. After a user-visible PR merges, the agent runs `pnpm release suggest`, asks the owner once, then `pnpm release ship <bump>` commits, pushes main and tags `vX.Y.Z` from the local machine (`palui-release`). PRs never bump the version. Never bump it by hand. While on `0.x`, the v1 scope is still being built; `1.0.0` means v1 is done, and only the owner calls that.

## Skills

Repo workflows live in `.agents/skills/` (`.claude/skills` links there). Use them instead of improvising:

- `palui-changelog`: add an Unreleased entry for a user-visible change.
- `palui-release`: pick the bump, cut the release, tag it.
- `palui-pr`: checks and conventions before opening a PR against `main`.
- `palui-new-connector`: add a connector end to end.
- `palui-schema-change`: change `schema.ts` and ship the migration safely.
- `palui-ui-check`: check a dashboard change against the fixtures.

## Fixture data (dev only)

`apps/app/src/dev/fixtures` holds five datasets for the dashboard: `demo`, `worst` (the realistic worst case for every rendered value), `empty`, `one` and `huge`. Fill a scratch database with one, then run the dashboard against it:

```bash
cd apps/app
DATA_DIR=/tmp/palui-demo pnpm tsx scripts/seed-demo.ts worst
DATA_DIR=/tmp/palui-demo pnpm dev
DATA_DIR=/tmp/palui-demo CONTROL_PORT=4891 pnpm tsx scripts/demo-runtime.ts   # optional stand-in for the runtime
```

On a database a fixture filled, the dev dashboard shows a "Demo data / Worst case / Empty / One / 1,000 rows" toggle at the bottom. It replaces every product row, so it refuses a database with data that didn't come from a fixture, and it isn't in a production build. Check a UI change against `worst` at 390 px and 768 px before calling it done. When a new field reaches the UI, add its worst case to `worst.ts`.
