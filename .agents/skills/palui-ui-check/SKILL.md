---
name: palui-ui-check
description: Check a Palui Agent dashboard change against fixture data (demo, worst, empty, one, huge) at phone and tablet widths before calling it done. Covers seeding a scratch database with the Go server, signing in without a dev bypass, the dev fixture toggle, and the localhost SSE stall that looks like a slow page. Use after any visible change in apps/app, when adding a field the dashboard renders, or when asked to "check the dashboard", "test the UI with worst-case data" or "screenshot the app".
---

# Palui dashboard check

A dashboard change isn't done until it holds up with the `worst` fixture at 390 px and 768 px wide. The fixtures live in the Go server, `apps/server/internal/fixtures/` (`demo`, `worst`, `empty`, `one`, `huge`). When a new field reaches the UI, add its worst realistic value to `worst.go` first (and its limit there if the field has a cap); the fixture goldens in `apps/server/testdata/fixtures-*.json` change with it.

Prefer the cheap check first: `npx tsc --noEmit` and `npx vitest run` in `apps/app`. Open a browser only when the question is visual.

## Run it

Use a scratch data dir, never `apps/server/data` or production. Check that ports 3000 and 4890 are free, then start both servers detached and keep their pidfiles. `palui dev serve` is the real server with stand-in connector runtimes: nothing connects to the network, and each connector keeps the status the fixture stored.

```bash
cd apps/server
DATA_DIR=/tmp/palui-ui go run ./cmd/palui dev seed worst
go build -o /tmp/palui-ui-bin ./cmd/palui
mkdir -p /tmp/claude-dev; DATA_DIR=/tmp/palui-ui nohup /tmp/palui-ui-bin dev serve --host 127.0.0.1 --port 3000 > /tmp/claude-dev/palui-server.log 2>&1 & echo $! > /tmp/claude-dev/palui-server.pid
cd ../app
nohup pnpm exec vite dev --port 4890 > /tmp/claude-dev/palui-app.log 2>&1 & echo $! > /tmp/claude-dev/palui-app.pid
```

Vite proxies `/api` and `/health` to the server on 3000. Read both logs for the real URLs before opening one. The server runs the real engine and scheduler, so overdue reminders and jobs in `worst` fire a few seconds after start; switch the fixture again with the toggle to reset them.

## Sign in

There's no dev login bypass. With the same `DATA_DIR`:

1. `DATA_DIR=/tmp/palui-ui /tmp/palui-ui-bin owner invite` prints a single-use link (15 min). Open it on the Vite address (`http://localhost:4890/app/invite/...`).
2. Press Continue, then "Skip for now" on the passkey step.
3. Apply the fixture from the toggle (or `POST /api/rpc/setFixture`) after signing in: running `dev seed` again under a running server confuses the stand-ins, and the invite link deletes the fixture's passkeys.

Sessions are stored in the database, so a server restart keeps you signed in. `APP_URL` defaults to `http://localhost:4890`, which is what the same-origin check expects behind the Vite proxy; an IP address there turns passkeys off.

## Look

- Use `agent-browser --session palui-ui` (never the default session), or a new Chrome MCP tab.
- The "Demo data / Worst case / Empty / One / 1,000 rows" toggle at the bottom of the dashboard swaps datasets in place. It only exists in a dev build and refuses a database that has real data.
- Load a page once and resize through 390, 768 and desktop widths. Don't reload per width: every full load opens an SSE stream, and after about six reloads the browser runs out of localhost connections and the next navigation stalls 3 to 24 s before the request is even sent. If a page seems to take 20 s, check `performance.getEntriesByType('navigation')[0].requestStart` before blaming the data.
- Take viewport screenshots, not full-page ones. Off-screen transcript messages use `content-visibility: auto` and come out blank, and the usage chart may not have drawn yet.
- Look for: text overflowing or pushing a row sideways, horizontal page scroll, truncation that hides the part that matters, empty states that say nothing, and numbers without separators.
- Status is a colored icon plus text, not a filled pill. Pills are only for real tags.

Visible changes need before and after screenshots for the PR (the `before-after-evidence` skill). Take the before shot before touching code.

## Clean up

```bash
bash ~/.claude/hooks/kill-tree.sh "$(cat /tmp/claude-dev/palui-app.pid)"
kill "$(cat /tmp/claude-dev/palui-server.pid)"
agent-browser --session palui-ui close
```
