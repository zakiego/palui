---
name: palui-new-connector
description: Add a new connector to Palui Agent (a channel, model provider, memory, tool source or skill) following the connector contract, with its Go manifest and runtime, tests, registry entry, the dashboard's manifest copy, the golden, dashboard fixtures and docs page. Use when asked to "add a connector", "integrate X into Palui", "add a new tool/skill/model provider/channel", or when changing what a connector exposes.
---

# New Palui connector

Read first: `apps/server/internal/connectors/define.go` (the contract) and PLAN section 4 in `docs/PLAN.md`. Copy the shape of the closest existing connector instead of inventing one. All Go paths below are under `apps/server/internal`.

| Kind | Closest example |
| --- | --- |
| `skill` with one tool, no setup | `connectors/webfetch/` |
| `tools` from a remote server, multiple instances | `connectors/mcp/` |
| `model` | `connectors/cliproxy/` |
| `memory` | `connectors/recall/` |
| OAuth | `connectors/gcal/` |
| `channel` | `connectors/telegram/` (a new channel also needs every place that switches on the channel name, and possibly a migration: see `palui-schema-change`) |

## Files

1. `connectors/<type>/manifest.go` (or the top of `<type>.go`): `var Manifest = connectors.Manifest{...}`.
   - `ConfigZod` is a `zod.Object(...)` of non-secret settings and `SecretZod` lists the secret fields; set `ParseConfig: kit.ParseConfig(ConfigZod)`, `ParseSecrets: kit.ParseSecrets(SecretZod)` and `SecretFields: kit.SecretNames(SecretZod)`. Write refusal texts as zod's issue text, since the dashboard shows the same field errors. Secrets are only stored through `connectors.Upsert` (encrypted) and the UI only ever sees them masked.
   - `Setup` steps drive the dashboard wizard. `PauseNote` says what pausing stops. `Trust: "external"` when results come from text anyone can write.
2. The runtime in the same package: `var Runtime = &connectors.Runtime{Manifest: Manifest, Test: ..., Start: ...}`.
   - `Test(c)` returns `connectors.TestResult` with a plain-words detail; it's what the "Test" button shows.
   - `Start(c)` returns a `*connectors.RunningConnector`. Stop work when `c.Ctx` is canceled. Report state with `c.SetStatus`, notable events with `c.Event`. Never read the clock or the environment directly: secrets come from `c.Secrets`, time from the injected clock or `now`.
   - `RunningConnector.Tools` returns tools keyed WITHOUT the namespace; the engine exposes them as `<connectorId>__<tool>`. Build them with `kit.Tool(description, schema, exec)` and give every tool a class: `read`, `write` or `destructive`. Anything you leave out is treated as `destructive` (fail closed). Mark tools whose results carry outside text as untrusted, so the turn gets tainted.
   - Network fetches the model can steer go through `safenet.Fetch` (the SSRF guard).
   - Each connector recovers its own panics through the supervisor; don't add a recover that hides errors from `SetStatus`.
3. `connectors/<type>/<type>_test.go`: use `conntest.NewCtx` (a fake context on `testutil.DB(t)`) and inject fakes for the network (`httptest`, `conntest.StartMCPServer`); never hit a real service. Cover `Test` success and failure, every tool's happy path, and the error text the user sees.
4. Register it in `connectors/registry/registry.go` (`Catalog()`), at the position the Connectors page should show it in its kind.
5. The dashboard's copy: `apps/app/src/shared/connectors/<type>/manifest.ts` (`defineManifest({...})`, importing only `zod` and `../define`), and its key in `apps/app/src/shared/connectors/manifests.ts` at the same position.
6. The golden: add the manifest object to `manifests` in `apps/server/testdata/connectors.json` at its catalog position, and its tools to `catalogs` (name, description, class, flags, input JSON schema). Both the Go registry tests and `apps/app/src/shared/manifest-golden.test.ts` compare against it, so they fail until the three agree. The goldens are frozen (the generators in `testdata/gen` don't run any more), so edit the JSON by hand.

## Around it

7. Dashboard fixtures: add the connector to `internal/fixtures/demo.go` and to `worst.go` with its worst realistic values (longest name, error status with a long detail), and give it a stand-in in `fixtures/standin.go` if `palui dev serve` should show tools for it. The fixture goldens (`testdata/fixtures-*.json`) change with them. Then follow `palui-ui-check` for its card and sheet.
8. Docs: add `apps/site/content/docs/connectors/<slug>.mdx` with the same sections as `web-fetch.mdx` (Connect, Settings, Tools, Safety, Pause and remove), add the slug to `connectors/meta.json`, and add a row to the table in `connectors/index.mdx`.
9. Changelog: an `### Added` bullet under Unreleased (`palui-changelog`).
10. Checks:
    ```bash
    (cd apps/server && gofmt -l . && go vet ./... && go test ./...)
    (cd apps/app && npx tsc --noEmit && npx vitest run)
    ```

## Don'ts

- Don't import anything heavier than `zod` into the dashboard's `manifest.ts`; it ships to the browser.
- Don't read secrets from the environment. They come from `c.Secrets`, set up through the dashboard.
- Don't add a connector-specific branch to the engine or supervisor. If the contract can't express it, change `define.go` deliberately and update every connector.
- Bot-facing copy (chat replies, button labels) is casual Indonesian. Dashboard and docs copy follow the existing pages.
