---
name: palui-ui-check
description: Check a Palui Agent dashboard change against fixture data (demo, worst, empty, one, huge) at phone and tablet widths before calling it done. Covers seeding a scratch database, signing in without a dev bypass, the dev fixture toggle, and the localhost SSE stall that looks like a slow page. Use after any visible change in apps/app, when adding a field the dashboard renders, or when asked to "check the dashboard", "test the UI with worst-case data" or "screenshot the app".
---

# Palui dashboard check

A dashboard change isn't done until it holds up with the `worst` fixture at 390 px and 768 px wide. The fixtures live in `apps/app/src/dev/fixtures/` (`demo`, `worst`, `empty`, `one`, `huge`). When a new field reaches the UI, add its worst realistic value to `worst.ts` first (and to `LIMITS` there if the field has a cap).

Prefer the cheap check first: `npx tsc --noEmit` and `npx vitest run` in `apps/app`. Open a browser only when the question is visual.

## Run it

Use a scratch data dir, never `./data` or production. Check the port first (`$CONDUCTOR_PORT` rules in the user's global instructions apply), then start the dev server detached and keep its pidfile:

```bash
cd apps/app
DATA_DIR=/tmp/palui-ui pnpm tsx scripts/seed-demo.ts worst
mkdir -p /tmp/claude-dev; DATA_DIR=/tmp/palui-ui nohup pnpm exec vite dev --port 4890 > /tmp/claude-dev/palui-app.log 2>&1 & echo $! > /tmp/claude-dev/palui-app.pid
```

Optional stand-in for the runtime, so runtime-backed panels render:

```bash
DATA_DIR=/tmp/palui-ui CONTROL_PORT=4891 nohup pnpm tsx scripts/demo-runtime.ts > /tmp/claude-dev/palui-rt.log 2>&1 & echo $! > /tmp/claude-dev/palui-rt.pid
```

Read the log for the real URL before opening it.

## Sign in

There's no dev login bypass. With the same `DATA_DIR`:

1. `pnpm cli owner invite` prints a single-use link (15 min).
2. Open it, press Continue, then "Skip for now" on the passkey step.
3. Seed again after the owner exists, or the fixture passkeys are skipped.

To keep the session across a server restart, run the server with a fixed `BETTER_AUTH_SECRET` (32+ characters) and a matching `APP_URL`. Put those in a script file written with the Write tool; a Bash command that mentions env files or exports secrets is blocked by the shell hook.

## Look

- Use `agent-browser --session palui-ui` (never the default session), or a new Chrome MCP tab.
- The "Demo data / Worst case / Empty / One / 1,000 rows" toggle at the bottom of the dashboard swaps datasets in place. It only exists in dev and refuses a database that has real data.
- Load a page once and resize through 390, 768 and desktop widths. Don't reload per width: every full load opens an SSE stream, and after about six reloads the browser runs out of localhost connections and the next navigation stalls 3 to 24 s before the request is even sent. If a page seems to take 20 s, check `performance.getEntriesByType('navigation')[0].requestStart` before blaming the data.
- Take viewport screenshots, not full-page ones. Off-screen transcript messages use `content-visibility: auto` and come out blank, and the usage chart may not have drawn yet.
- Look for: text overflowing or pushing a row sideways, horizontal page scroll, truncation that hides the part that matters, empty states that say nothing, and numbers without separators.
- Status is a colored icon plus text, not a filled pill. Pills are only for real tags.

Visible changes need before and after screenshots for the PR (the `before-after-evidence` skill). Take the before shot before touching code.

## Clean up

```bash
kill $(cat /tmp/claude-dev/palui-app.pid) $(cat /tmp/claude-dev/palui-rt.pid 2>/dev/null) 2>/dev/null
agent-browser --session palui-ui close
```
