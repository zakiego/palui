# MORNING

First thing: do "Needs you" step 1 (clone + `.env`), then steps 2 to 4 get Palui live at palui.zakiego.com in about 15 minutes.

Everything is on branch `feat/v1` (pushed). No PR opened, as asked. `main` only has the plan.

## Done

All of M0 to M6 is in code with tests. 529 tests pass, typecheck and build are green at the tip.

| Package | Tests |
|---|---|
| core (engine, reminders, jobs, approvals, budgets, outbox, connectors, hooks, SSRF) | 271 |
| connectors (13 connectors) | 145 (+7 live tests, skipped by default) |
| app (dashboard, auth, CLI, routes) | 74 |
| runtime (control API, main, deps) | 12 |
| edge (Worker routing) | 11 |
| db | 9 |
| site | 7 |

- Docker image builds (394 MB). `docker compose up --wait` goes healthy in about 11 s, both services.
- Web chat works end to end in the compose stack against CLIProxyAPI. Proof: `docs/screenshots/e2e-web-chat-desktop.png` and this runtime log line:
  `{"msg":"turn finished","run_id":"run_0mw9mohbztawr1ufoue","status":"ok","steps":2,"tokens_in":5457,"tokens_out":235,"ms":4768,"tainted":false,"connector_id":"cliproxy"}`
- Reminder parse eval, live against CLIProxyAPI (claude-haiku-4-5): 60/60 (Indonesian 30/30, English 30/30). Pass mark 95%.
- 42 screenshots in `docs/screenshots/`: every dashboard page at desktop and 390 px, the 3 Connectors page variants, a sheet, empty states, the 3 landing heroes, a docs page, and the end-to-end chat and approval flow.
- Cold reviews by fresh agents found 7 HIGH and about 28 MEDIUM issues. All fixed with regression tests. A live test then caught one more (a write tool in the same step as a web fetch skipped approval). Fixed too. A last review of the fix commits found no HIGH and 2 MEDIUM (a crash-looping message, a timed-out send that could arrive twice). Both fixed.

## Verified live vs fakes

Live (real services on this mini):
- AI SDK tool calling through CLIProxyAPI from an OrbStack container: 5/5 round trips for haiku and sonnet, both protocols.
- Web chat turn with a reminder tool call, in Docker.
- Taint + approval: web fetch then a write tool asks first, the approval survives a runtime restart, then resumes (`docs/screenshots/e2e-approval-*.png`).
- Inbound hook: signed 202, replay 409, bad signature 401, stale timestamp 401.
- recall contract test, read-only: 6/6 against 127.0.0.1:4800. Nothing was written to your recall.

Only with fakes:
- Telegram (grammY test transport), WhatsApp (mocked Baileys socket). No bot or device exists yet.
- MCP (local test server), Google Calendar (mocked Google), ElevenLabs voice (mocked).
- Passkey registration: WebAuthn hangs in the headless browser, so I signed in through the invite link and "Skip for now".
- Backup to R2 and the restore drill: written, never run (no `age` on this machine, no R2 bucket).

## Picks for you to confirm

- Connectors page: Cards (`apps/app/DESIGN.md`). Open `/app/connectors?v=2` (Rows) or `?v=3` (Board) to compare. The pick lives in `apps/app/src/components/connectors/layouts.ts`.
- Landing: you picked it on 2026-11-10 (dark terminal, Mono palette, "An agent friend you host yourself"). Nothing left to confirm there.
- Dashboard accent: Banjar sasirangan indigo, oklch(0.45 0.15 270) light, oklch(0.7 0.13 270) dark. The site no longer uses it, so say if the dashboard should go Mono too.

## Assumptions

- pnpm 12.8.1 (this machine's) in `packageManager`, so Docker and local share one lockfile format.
- Dashboard sign-in is passkey only. The invite link creates the owner (username `owner`, random internal password nobody knows), signs in server side, then asks for a passkey. A new invite is the recovery path and removes old passkeys and sessions.
- Model roles: `default` and `vision` = claude-sonnet-4-6, `fast` = claude-haiku-4-5, each with ai-gateway as the fallback.
- Prod port 4802 (what `free-port.sh` returns). The control port 4891 is never published.
- Reminder defaults: pagi 08:00, siang 12:00, sore 16:00, malam 20:00. A bare hour from 1 to 6 means PM. A one-off earlier today rolls to tomorrow, and the echo says "Besok" so a wrong guess shows.
- Heartbeat: every 30 min from 08:00 to 22:00, `fast` model, 100k tokens a day. Briefing: 07:00, `default` model, 50k a day.
- WhatsApp pairing code goes to the number being linked (utusan's), entered in the wizard under "Number to link", not your own number. Leave it empty to pair with the QR instead. Otherwise your own messages would all be `fromMe` and dropped.
- New WhatsApp DMs are only answered when utusan doesn't bind that chat: Palui reads utusan's config if mounted, otherwise you tick "This chat isn't bound in utusan" in the wizard. The mount line in `compose.yaml` is commented out on purpose (it would expose nothing else, but it's your call).
- Non-owner messages are dropped before they're stored. New WhatsApp groups start not allowed (v1 is DM only).
- MCP tools you haven't reviewed count as destructive, so they always ask first.
- Calendar results and external MCP servers taint the turn (invite titles come from anyone).
- Hooks are signed over `<x-palui-timestamp>.<raw body>` (HMAC-SHA256, 5 minute window, replays refused).
- CLIProxyAPI's live config is `/opt/homebrew/etc/cliproxyapi.conf`, not `~/.cli-proxy-api/config.yaml`. I copied the client key to `~/.config/palui/cliproxy-key` (mode 600, outside the repo) for the live tests. Delete it whenever you like.

## Known gaps

1. No Resend email when a connector stays in error and no channel is up. It logs an event only.
2. Gemini is not served by CLIProxyAPI right now, so there's no Gemini model and no Gemini voice fallback. Voice-in is ElevenLabs only.
3. Round-robin vs sticky routing tokens weren't measured (changing CLIProxyAPI config was off limits). Your live config already has `session-affinity: true`.
4. No Baileys pairing spike in a container (pairing was off limits). I did check that Baileys loads inside the image.
5. Lighthouse wasn't run (not installed). OG, robots, sitemap and canonical URLs are in place.
6. No retention pruning yet for media and `tool_calls` (events are pruned at 90 days).
7. A Telegram bot stuck in a 409 loop flips error/on on each restart, which resets the 5 minute alert timer.
8. Settings and parts of the reminder and job forms are still English-only in the `id` locale.
9. If a stricter recurrence check ever rejects a stored series, it quietly ends after its next fire (only a log line). No real data exists yet, so no migration check was added.
10. D8: the 200 MB log cap went into `~/.cli-proxy-api/config.yaml`, but the running service reads `/opt/homebrew/etc/cliproxyapi.conf`, which still says `logs-max-total-size-mb: 0`.

## Needs you

1. Clone and create the secrets file:
   ```bash
   gh repo clone zakiego/palui ~/selfhost/palui && cd ~/selfhost/palui && git checkout feat/v1
   umask 077 && nano .env
   ```
   Put in three keys: `MASTER_KEY=` (from `openssl rand -base64 32`, also save it in your password manager), `BETTER_AUTH_SECRET=` (another `openssl rand -base64 32`), `APP_URL=https://palui.zakiego.com`.
   deploy.sh runs `git pull --ff-only` on whatever branch is checked out. Done 2026-11-11: `~/selfhost/palui` is on `main`.
2. Port and deploy: `~/.agents/skills/zaki-selfhost/scripts/free-port.sh` (should print 4802, else edit `compose.yaml`), then `~/.agents/skills/zaki-selfhost/scripts/deploy.sh palui`.
3. Hostname: `~/.agents/skills/zaki-selfhost/scripts/add-host.sh palui.zakiego.com 4802 "Palui Agent"`, then restart the tunnel in Terminal: `sudo launchctl kickstart -k system/com.cloudflare.cloudflared`.
4. Owner and model:
   ```bash
   cd ~/selfhost/palui
   docker compose exec app palui owner invite        # open the link, add a passkey
   docker compose exec -T app palui connectors connect cliproxy --secret-stdin apiKey < ~/.config/palui/cliproxy-key
   ```
   Say hi in Chats.
5. Telegram: in Telegram, @BotFather, `/newbot`, copy the token. Dashboard, Connectors, Telegram, paste, Connect, open the claim link, press Start. (Or `docker compose exec -T app palui connectors connect telegram --secret-stdin botToken < token.txt`, then get the claim link in the sheet.)
6. WhatsApp (utusan's number, as a new linked device): first remove your DM's binding from `~/.utusan/utusan.json`. Dashboard, Connectors, WhatsApp: save your phone as owner, tick "This chat isn't bound in utusan", press Start pairing, then type utusan's number under "Number to link" and press Get code. On utusan's phone: WhatsApp, Linked devices, Link with phone number, enter the 8-character code.
7. Done 2026-11-10: landing and docs are live at https://palui.zakiego.com (the `palui-edge` Worker on `palui.zakiego.com/*`, sending site paths to Pages). Until steps 2 and 3 run, `/app`, `/api` and `/health` fall through to the `*.zakiego.com` wildcard and answer 404. Since 2026-11-11 the site is the git-connected Pages project `palui-web`, which builds `apps/site` on every push to `main`, so there's nothing to deploy by hand. Build logs:
   ```bash
   CF="$HOME/.agents/skills/cf-zakiego-lab/scripts/cf-zakiego-lab"
   "$CF" pages deployments list --project-name palui-web
   ```
8. Done 2026-11-11 (hook id `710181508`). Auto-deploy: add `"zakiego/palui": "palui"` to `~/.agents/skills/zaki-selfhost/webhook/apps.json`, `launchctl kickstart -k gui/$(id -u)/id.zakiego.selfhost-webhook`, then create the GitHub hook with the command in the zaki-selfhost skill ("Add a repo", step 3).
9. Monitoring: add `https://palui.zakiego.com/health` to the `http` list in `~/.agents/skills/m4-alerts/config.default.json`.
10. Done 2026-11-11, and the restore drill passed. Backups: `brew install age`, `age-keygen -o /tmp/palui.key`, put the public key in `~/.config/palui/age-recipient` (`age-keygen -y /tmp/palui.key`), store only the `AGE-SECRET-KEY-` line in the Keychain as `palui-backup-age-identity` (`security add-generic-password -s palui-backup-age-identity -a palui -w "$(grep AGE-SECRET-KEY /tmp/palui.key)"`), `rm /tmp/palui.key`, create the R2 bucket `palui-backups`, then `ops/install-backup.sh`. Test with `scripts/restore-drill.sh`. Don't store the whole key file: the Keychain returns a multi-line value as hex, and age can't read that back. Copy the secret key into your password manager too, since the Keychain is its only copy.
11. Optional: recall memory (make a `rcl_` key in recall for Palui, then Connectors, recall) and MCP presets (vorneo, keuangan, todomap, linear: token only).
12. Optional: fix the D8 log cap in `/opt/homebrew/etc/cliproxyapi.conf` (gap 10).
