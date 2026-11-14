# Golden generators (frozen)

These scripts exported the golden files in `apps/server/testdata` from the TypeScript backend: crypto vectors, ids, the seeded database, time and recurrence cases, chat and flow traces, connector manifests and tool catalogs, channel cases, passkeys, the RPC calls and the fixture datasets. The Go tests compare the Go code against those files.

They were run against the TypeScript backend at commit `2c89fa5`, the last commit before the Go port removed `apps/runtime`, `packages/core`, `packages/connectors`, `packages/db` and the dashboard's server code. They import those packages by relative path, so they only run again in a checkout of that commit (from `apps/app` there, with `pnpm tsx ../server/testdata/gen/<script>.ts`, as each script's header says).

The goldens are frozen from now on. The Go code is the implementation, and a behavior change updates the Go test or the golden file by hand in the same change. Nothing in the workspace compiles, typechecks or tests these scripts: they sit outside every `tsconfig.json` and vitest `include`, and Go ignores them.
