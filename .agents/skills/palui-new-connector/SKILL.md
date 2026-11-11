---
name: palui-new-connector
description: Add a new connector to Palui Agent (a channel, model provider, memory, tool source or skill) following the connector contract, with its manifest, runtime, tests, registry entries, dashboard fixtures and docs page. Use when asked to "add a connector", "integrate X into Palui", "add a new tool/skill/model provider/channel", or when changing what a connector exposes.
---

# New Palui connector

Read first: `packages/core/src/connectors/define.ts` (the contract) and PLAN section 4 in `docs/PLAN.md`. Copy the shape of the closest existing connector instead of inventing one:

| Kind | Closest example |
| --- | --- |
| `skill` with one tool, no setup | `packages/connectors/src/webfetch/` |
| `tools` from a remote server, multiple instances | `packages/connectors/src/mcp/` |
| `model` | `packages/connectors/src/cliproxy/` |
| `memory` | `packages/connectors/src/recall/` |
| OAuth | `packages/connectors/src/gcal/` |
| `channel` | `packages/connectors/src/telegram/` (a new channel also widens `Channel` in `packages/db/src/schema.ts`, which is a contract change: see `palui-schema-change`) |

## Files

1. `packages/connectors/src/<type>/manifest.ts`: `export default defineManifest({...})`. It may import only `zod` and `@palui/core/connectors/define`, because the dashboard bundles every manifest. No SDKs, no node built-ins.
   - `config` is a zod object of non-secret settings, `secrets` maps each secret field to a zod schema. Secrets are only stored through `upsertConnector` (encrypted) and the UI only ever sees them masked.
   - `setup` steps drive the dashboard wizard. `pauseNote` says what pausing stops. `trust: "external"` when results come from text anyone can write.
2. `packages/connectors/src/<type>/runtime.ts`: `export default defineRuntime(manifest, { test, start })`. Runtime-only imports go here.
   - `test(ctx)` returns `{ ok, detail }` in plain words; it's what the "Test" button shows.
   - `start(ctx)` returns a `RunningConnector`. Stop work on `ctx.signal`. Report state with `ctx.setStatus`, notable events with `ctx.event`.
   - Tools come back from `tools(turn)` keyed WITHOUT the namespace; the engine exposes them as `<connectorId>__<tool>`. Give every tool a `class`: `read`, `write` or `destructive`. Anything you leave out is treated as `destructive` (fail closed). Set `untrusted: true` on tools whose results carry outside text, so the turn gets tainted.
   - Network fetches the model can steer go through `safeFetch` from `@palui/core/net/fetch` (the SSRF guard).
3. `packages/connectors/src/<type>/runtime.test.ts`: vitest next to the code. Use `fakeCtx` from `../mcp/test-helpers` and inject fakes for the network; never hit a real service in tests. Cover `test()` success and failure, every tool's happy path, and the error text the user sees.
4. Register it in both `packages/connectors/src/manifests.ts` and `packages/connectors/src/runtimes.ts`, same key, same position. Key order is the catalog order on the Connectors page.

## Around it

5. Dashboard fixtures: add the connector to `apps/app/src/dev/fixtures/demo.ts` and to `worst.ts` with its worst realistic values (longest name, error status with a long detail). Then follow `palui-ui-check` for its card and sheet.
6. Docs: add `apps/site/content/docs/connectors/<slug>.mdx` with the same sections as `web-fetch.mdx` (Connect, Settings, Tools, Safety, Pause and remove), add the slug to `connectors/meta.json`, and add a row to the table in `connectors/index.mdx`.
7. Changelog: an `### Added` bullet under Unreleased (`palui-changelog`).
8. Checks:
   ```bash
   (cd packages/connectors && npx tsc --noEmit && npx vitest run)
   (cd apps/app && npx tsc --noEmit && npx vitest run)
   (cd apps/runtime && npx tsc --noEmit)
   ```

## Don'ts

- Don't import a runtime-only package from `manifest.ts`. The app build pulls it into the browser bundle or fails.
- Don't read secrets from the environment. They come from `ctx.secrets`, set up through the dashboard.
- Don't add a connector-specific branch to the engine or supervisor. If the contract can't express it, change `define.ts` deliberately and update every connector.
- Bot-facing copy (chat replies, button labels) is casual Indonesian. Dashboard and docs copy follow the existing pages.
