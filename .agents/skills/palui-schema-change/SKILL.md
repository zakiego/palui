---
name: palui-schema-change
description: Change the Palui Agent SQLite data model safely. Edits packages/db/src/schema.ts, generates the Drizzle migration and the bundled migrations.gen.ts, keeps old rows working, and records the rollback impact in the changelog. Use when adding or changing a table, column, index or enum in Palui, when a feature needs new stored data, or when tests fail with a schema mismatch.
---

# Palui schema change

`packages/db/src/schema.ts` is a contract (PLAN 9). The app and the runtime share one SQLite file, only the app runs migrations, and the runtime refuses to start on a schema it doesn't know. A rollback past a migration makes `/health` answer 503 until the pre-deploy snapshot is restored. So every migration is a one-way door: make each one small and additive.

## Steps

1. Edit `packages/db/src/schema.ts`. Prefer additive changes:
   - New column: nullable or with a default, so rows written by the previous build still read.
   - New table or index: fine.
   - Rename or drop: don't do it in one step. Add the new column, ship a release that writes both and reads the new one, and drop the old one in a later release.
   - New enum value (a `text` column checked in TS, like `CONNECTOR_KINDS` or `Channel`): update every `switch` and lookup that handles it; `tsc` finds most of them.
2. Generate the migration and the bundle. This writes `packages/db/drizzle/NNNN_<name>.sql`, updates `drizzle/meta/`, and regenerates `src/migrations.gen.ts`:
   ```bash
   pnpm --filter @palui/db db:generate
   ```
   Never edit `migrations.gen.ts` or an already merged `.sql` file by hand. If the generated SQL is wrong, fix the schema and generate again before merging.
3. Read the generated SQL. SQLite can't alter most column properties, so Drizzle may rebuild a table (`__new_<table>`, copy, drop, rename). That's fine for small tables; for `messages` or `events` check that it copies every column and keeps the indexes.
4. Update the code that reads and writes the data in `packages/core`, and test it with `memoryDb()` from `@palui/db` (a migrated in-memory DB). Pass `now` explicitly instead of faking timers.
5. If the new field reaches the dashboard, add its worst case to `apps/app/src/dev/fixtures/worst.ts` (and `LIMITS` there if it has a cap), then follow `palui-ui-check`.
6. Changelog (`palui-changelog`): describe the feature, and add "Adds a migration; rolling back past this release needs the pre-deploy snapshot." The release that carries it is at least a minor bump.
7. Checks:
   ```bash
   (cd packages/db && npx tsc --noEmit && npx vitest run)
   (cd packages/core && npx tsc --noEmit && npx vitest run)
   (cd apps/app && npx tsc --noEmit && npx vitest run)
   (cd apps/runtime && npx tsc --noEmit && npx vitest run)
   ```

## Two branches, two migrations

Migration files are numbered. If main gained a migration since you branched, rebase, delete your generated `.sql` and its `meta` snapshot and journal entry, and run `db:generate` again so yours comes after. Two migrations with the same number never merge.
