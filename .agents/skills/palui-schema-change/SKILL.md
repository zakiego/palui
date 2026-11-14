---
name: palui-schema-change
description: Change the Palui Agent SQLite data model safely. Adds a handwritten SQL migration to the Go server (apps/server/internal/db/migrations), updates the row types in schema.go, keeps old rows working, and records the rollback impact in the changelog. Use when adding or changing a table, column, index or enum in Palui, when a feature needs new stored data, or when tests fail with a schema mismatch.
---

# Palui schema change

The schema is a contract (PLAN 9). It lives in two places in `apps/server/internal/db`: `migrations/*.sql` (handwritten SQL, embedded in the binary, applied in name order at start) and `schema.go` (the row types, their column lists and scan functions). The server records each applied migration in `_migrations`. A binary that finds a migration it doesn't know reports the schema as `newer`, keeps the runtime off and answers `/health` with 503, so a rollback past a migration needs the pre-deploy snapshot. Every migration is a one-way door: make each one small and additive.

## Steps

1. Decide the change. Prefer additive ones:
   - New column: nullable or with a default, so rows written by the previous build still read.
   - New table or index: fine.
   - Rename or drop: don't do it in one step. Add the new column, ship a release that writes both and reads the new one, and drop the old one in a later release.
   - New enum value (a `text` column checked in code, like connector kinds or channel names): update every `switch` and lookup that handles it; `grep` for the existing values.
2. Write the migration: `apps/server/internal/db/migrations/NNNN_<short_name>.sql`, numbered one past the highest file there, in lowercase snake case. Separate statements with a line `--> statement-breakpoint` (the runner splits on it; the whole file runs in one transaction). Never edit a migration that has been merged: fix it with a new one.
3. SQLite can't alter most column properties. When you must rebuild a table, do it by hand (`CREATE TABLE __new_<table>`, `INSERT INTO ... SELECT` naming every column, drop, rename, recreate the indexes) and check that every column and index survives, especially for `messages` and `events`.
4. Update `schema.go`: the row struct (fields in table order with camelCase JSON tags), its `XCols` column list and `ScanX`. Queries always name their columns. JSON columns use `db.JSON`, `db.NullJSON` or `db.RawJSON`, booleans `db.Bool`, timestamps Unix ms.
5. Update the code that reads and writes the data, and test it on `testutil.DB(t)` (a migrated in-memory database). Pass `now` explicitly. Writes inside `d.Tx(func(tx db.Q) error {...})` use only `tx`.
6. Old data: `testdata/seeded.db` is a database from before the Go port. Tests that open a copy of it (the RPC and fixture goldens) prove the migration applies on real old rows; run them. If the change alters a stored or returned shape on purpose, update the golden JSON in `testdata` by hand in the same change (the generators are frozen).
7. If the new field reaches the dashboard, add its worst case to `apps/server/internal/fixtures/worst.go` (and its limit there if it has a cap) and the type in `apps/app/src/lib/api`, then follow `palui-ui-check`.
8. Changelog (`palui-changelog`): describe the feature, and add "Adds a migration; rolling back past this release needs the pre-deploy snapshot." The release that carries it is at least a minor bump.
9. Checks:
   ```bash
   (cd apps/server && gofmt -l . && go vet ./... && go test ./...)
   (cd apps/app && npx tsc --noEmit && npx vitest run)
   ```

## Two branches, two migrations

Migration files are numbered. If main gained a migration since you branched, rebase and renumber yours so it comes after. Two migrations with the same number never merge. Always append: a file numbered below one production already applied would still run, but after it, so the repo's order would no longer be the order it ran in.
