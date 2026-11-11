# PLAN: Palui Agent

Status: v2, 2026-11-10. Draft v1 had a cold review (25 findings, 6 HIGH), and every finding is applied below. The HIGH claims were checked against source: `deploy.sh:57` (`up --wait` waits on every service), recall `better-auth.ts:68` (`disableSignUp: true`), recall `config.ts:15` (default container `"default"`), `~/.cli-proxy-api/config.yaml` (`routing.strategy: round-robin`, `logs-max-total-size-mb: 0`).

## 0. Name

- Display name: Palui Agent. Domain, CLI, package and repo are plain `palui` (`palui.zakiego.com`, `github.com/zakiego/palui`, private).
- Named after Si Palui, the Banjar folk comic hero from Kalimantan Selatan: simple-looking but sharp. Pronounced PAH-loo-ee.
- A web search found no AI product or GitHub repo named palui. That isn't a formal trademark check.
- It doesn't clash with utusan or recall. Tool names may use Indonesian verbs, the way utusan's do (`ingatkan`, `kirim`).

## 1. What it is

A self-hosted personal AI assistant in the spirit of OpenClaw and Hermes. I talk to it from WhatsApp and Telegram. It remembers through recall, reminds me of things, runs scheduled jobs, and reaches tools through MCP. Models come through CLIProxyAPI, so my existing Claude and Gemini subscriptions pay for it.

What makes it different from OpenClaw: every capability is a connector, and every connector can be switched on, paused or removed from one screen or one chat command, with no restart.

For now only I can use it. Anyone else who messages the bot gets nothing.

### Goals for v1

1. Chat with it on Telegram and WhatsApp, in DMs, in Indonesian or English.
2. Reminders that fire, survive restarts and deploys, and never get lost on a failed send.
3. Scheduled agent jobs ("every day at 07:00, brief me").
4. recall as long-term memory.
5. Any HTTP MCP server as a tool source, with per-tool on/off.
6. CLIProxyAPI (and the existing ai-gateway) as model providers, swappable, with failover.
7. Connecting or disconnecting anything takes under a minute.
8. Landing page and docs on Cloudflare Pages.

### Parity with OpenClaw and Hermes

| Feature | OpenClaw / Hermes | Palui v1 |
|---|---|---|
| Owner allowlist / pairing | yes | yes (owner only, claimed with a nonce) |
| Persona file (SOUL.md) | yes | yes, `settings.persona` |
| Cron | yes | yes, Jobs |
| Heartbeat | OpenClaw | yes, as a Job every 30 min with a "say nothing if nothing matters" prompt |
| Model failover | yes | yes, per model role |
| Approvals for risky tools | yes | yes, saved and resumable (section 8) |
| Self-written skills | Hermes | deferred |
| Subagents | both | deferred |
| Group chats | both | deferred (the code path exists, the UI is hidden) |
| Voice replies (TTS) | both | deferred. Voice-in only |
| Multiple profiles or agents | both | deferred |
| stdio MCP, shell, browser | both | deferred (needs binaries in the container and a sandbox) |

### Non-goals for v1

Multi-user, sign-up, billing. Everything marked deferred above. Native app. Replacing utusan, which keeps running. Any migration is a separate decision later (D3).

## 2. What already exists and what we reuse

| Thing | Where | How Palui uses it |
|---|---|---|
| utusan | `~/Code/JS/Node/2026/utusan` | Ideas, not imported code. Reused: the adapter contract, router outcomes (`ignore` with a reason sentence), fail-closed tool classes (unmatched means `destructive`), hot reload with a last-good config, the SSRF guard, phone JID vs LID handling (`adapters/types.ts`), and Baileys 440/428/reconnect handling. Avoided: reminders deleted before the send, non-atomic `threads.json` writes, container hostname used as the session id. |
| recall | `recall.zakiego.com`, `~/selfhost/recall` | Memory connector (MCP for tools, REST for auto-recall and auto-save). Also the repo template: pnpm monorepo, TanStack Start + Nitro, Next 16 static Fumadocs site, edge Worker routing, Better Auth passkeys with CLI-only users, Drizzle + SQLite, pre-deploy DB snapshot, shadcn `base-nova` (`baseColor: neutral`), Geist, oklch tokens. |
| CLIProxyAPI | Homebrew `cliproxyapi`, `127.0.0.1:8317` on the mini | Model connector. Claude-format `/v1/messages`, OpenAI-format `/v1/chat/completions`, `/v1/models`. Accounts: Claude (4), Antigravity/Gemini, OpenRouter. It has no built-in usage stats since v6.10.0. |
| ai-gateway | `127.0.0.1:8793` | Second model connector and the fallback. |
| zaki-selfhost | skill | Docker in OrbStack, port 4800 to 4899, tunnel, `deploy.sh`, auto-deploy webhook, m4-alerts. |
| Cloudflare Pages (Zakiego Lab) | `cf-zakiego-lab` | Landing and docs. |

## 3. Architecture

```
 WhatsApp (Baileys)    Telegram (grammY)    Web chat (dashboard)
          \                   |                    /
           +--------- channel connectors ---------+
                              |
                 INBOX (messages, status=pending)
                              v
  +---------------------- runtime ---------------------------------+
  |  router -> per-chat queue -> turn                               |
  |    turn = session + memory + prompt -> AI SDK loop -> OUTBOX    |
  |    approval needed -> pending_approvals, turn ends              |
  |  scheduler (reminders, jobs) -> OUTBOX or a turn                |
  |  outbox worker: claim, send, ack, retry                         |
  |  connector supervisor: reconcile desired vs running                |
  +-----------------------------------------------------------------+
        |  SQLite WAL on volume  |  control API (compose network only)
  +---------------------- app -------------------------------------+
  |  TanStack Start: dashboard, REST API, auth, migrations, /health |
  +-----------------------------------------------------------------+
        |
  CLIProxyAPI / ai-gateway (host)   recall (host :4800)   MCP servers
```

### 3.1 Two services, one image

- `app`: TanStack Start (Nitro). Dashboard, REST API, auth, `/health`. It's the only service that runs migrations.
- `runtime`: a plain Node process. Channel sockets, turns, scheduler, outbox, supervisor.

Why the split: long-lived sockets (Baileys, grammY polling) don't belong in Nitro's bundle or its dev reload, and the runtime should stay small and boring. The split doesn't keep the socket up through deploys, since both restart on each push. The durable inbox and outbox handle deploys instead (section 8).

Connector code is split so the app never bundles Baileys:

- `packages/connectors/<id>/manifest.ts`: zod schemas, UI metadata, setup steps. The app and runtime both import it.
- `packages/connectors/<id>/runtime.ts`: `test`, `start`, `tools`. Only the runtime imports it. The dashboard's Test button goes through `POST /control/test/:id`.

`compose.yaml` rules (deploy.sh runs `up -d --build --wait`, which waits on every service):

- Only `app` has `build:`. `runtime` uses `image: palui:latest` with `pull_policy: never`. M0 checks that compose builds it once and that `runtime` uses it.
- The `runtime` healthcheck is liveness only: event loop responsive, DB open, schema version known. Connector state goes in the `/health` JSON body and m4-alerts, never in the exit code. A WhatsApp outage must not fail a deploy and trigger a rollback.
- `runtime` has `depends_on: { app: { condition: service_healthy } }` and refuses to start on a schema it doesn't know.
- `stop_grace_period: 60s` on both. The runtime drains on SIGTERM: stop taking new turns, finish the current step, release outbox claims.
- `.dockerignore` excludes `apps/site`, `apps/edge` and docs. A docs commit then produces the same image layers and compose doesn't restart the containers.
- deploy.sh only prints `app` logs on failure. The docs say to run `docker compose logs runtime` too.
- The control port is never published. `app` is bound to 127.0.0.1.

### 3.2 Stack

| Layer | Choice |
|---|---|
| Repo | pnpm monorepo: `apps/app`, `apps/runtime`, `apps/site`, `apps/edge`, `packages/core`, `packages/db`, `packages/connectors` |
| Runtime | Node 24, TypeScript, bundled with tsdown |
| Agent loop | AI SDK 7. The default role uses `@ai-sdk/anthropic` with `baseURL` on CLIProxyAPI `/v1/messages` and cache breakpoints on system and tools. `@ai-sdk/openai-compatible` covers Gemini and others. `@ai-sdk/mcp` for MCP clients. Exact APIs come from `node_modules/ai/docs` at build time. |
| WhatsApp | Baileys 7, the same pinned rc as utusan |
| Telegram | grammY, long polling |
| DB | SQLite, better-sqlite3 + Drizzle, WAL, `busy_timeout` |
| Time | Luxon (or Temporal if Node 24 ships it stable) for wall-clock math in the stored zone |
| Auth | Better Auth, passkeys, `disableSignUp: true` from boot |
| Dashboard | React 19, Tailwind 4, shadcn `base-nova`, `sidebar-07`, Sonner, lucide |
| Site | Next 16 static export + Fumadocs on Pages |
| Edge | Worker on `palui.zakiego.com`, copied from recall's `apps/edge/src/routing.ts` |

### 3.3 Hostnames and ports

- `palui.zakiego.com`: the edge Worker. `/app`, `/api`, `/api/auth` and `/health` go to the tunnel, everything else goes to Pages.
- Tunnel: one rule to `127.0.0.1:<port>` from `free-port.sh` (likely 4802).
- Inside OrbStack: CLIProxyAPI is `http://host.docker.internal:8317`, ai-gateway is `:8793`, recall is `:4800`. That skips the tunnel and Cloudflare's 100 s limit.

## 4. Connectors

Everything optional is a connector of kind `channel`, `model`, `memory`, `tools` or `skill`. They all share one manifest shape, one lifecycle and one UI.

### 4.1 Manifest

```ts
// packages/connectors/telegram/manifest.ts
export default defineManifest({
  id: "telegram",
  kind: "channel",
  name: "Telegram",
  icon: "send",
  summary: "Chat with Palui from a Telegram bot.",
  config: z.object({}),
  secrets: { botToken: z.string().regex(/^\d+:[\w-]{30,}$/) },
  setup: [
    { field: "botToken", label: "Bot token", help: "From @BotFather, /newbot" },
    { action: "claimOwner", label: "Open the link and press Start" },
  ],
});

// packages/connectors/telegram/runtime.ts
export default defineRuntime(manifest, {
  test: async (ctx) => ({ ok: true, detail: "@palui_bot reachable" }),
  start: async (ctx) => ({ stop: async () => {} }),
  tools: (ctx) => ({}),
});
```

Adding a connector means adding one folder. The dashboard card, setup wizard, status, chat commands and docs page skeleton are all generated from the manifest.

### 4.2 States

```
not set up --connect--> connecting --ok--> on
                            |               |  pause (keeps credentials)
                            v               v
                         error <------- paused --resume--> connecting
                            |
  any state --remove (confirm)--> not set up (deletes credentials, unlinks device)
```

- Pause: the Switch. Instant, reversible, no confirmation. A Sonner toast offers Undo.
- Remove: in the danger area of the connector sheet, behind an AlertDialog that names what gets lost ("This unlinks the WhatsApp device. You'll need to pair again.").

### 4.3 Edge-case rules

| Situation | Rule |
|---|---|
| Pausing mid-turn | Applies from the next turn. Tool calls already running finish. |
| Pausing the Reminders skill | Hides its tools only. Reminders already set still fire. The card says so. |
| The only model connector is paused | The router replies with fixed text: "Model lagi off. Nyalakan: `/connectors on cliproxy`". Plain reminders and commands keep working. |
| Channel paused with outbox rows waiting | Rows wait without using up attempts. They send on resume. |
| Channel removed with rows waiting | Rows go to the fallback channel if one is on. Otherwise they're cancelled, with an event logged. |
| `/connectors off telegram` sent from Telegram | Allowed only when another channel or the dashboard is reachable. The reply says how to undo it. Otherwise it's refused. |
| recall auto-recall times out | The turn goes on without memory. The card shows the skip rate ("skipped 4 of last 20"). |
| Connector throws repeatedly | `error` state, restarts with backoff (5 s, 30 s, 2 min, 10 min cap). After 5 min in `error`, I get a message on another channel. |

### 4.4 Supervisor

The desired state is the `connectors` rows (`enabled`, `config_hash`). The running state is the handles held in memory. It reconciles when the control API pokes it and every 30 s. Each connector is isolated, so a throw never takes the process down. Status changes go to `events`, so the dashboard, `/health` and chat all read the same thing.

### 4.5 Secrets

Secrets are entered in the dashboard only and encrypted at rest with AES-256-GCM using `MASTER_KEY` from `.env`. They're write-only, shown as `rcl_••••a91f`: they can be replaced but never revealed. Logs redact `rcl_`, `zkg_`, `sk-` and bot-token patterns.

### 4.6 Three ways to flip a connector

1. Dashboard: the card Switch (pause/resume) and the sheet (connect, configure, test, remove).
2. Chat, owner only: `/connectors` lists every connector with its state (Telegram shows inline buttons), plus `/connectors off recall` and `/connectors on recall`. Connect and remove stay in the dashboard.
3. CLI: `docker compose exec app palui connectors list|on|off <id>`.

## 5. Connectors in v1

### 5.1 Channels

Telegram (the first channel built)
- Connect: paste the token. Test calls `getMe` and shows `@bot_username`. The wizard then shows `t.me/<bot>?start=<nonce>` as a link and a QR. The nonce is single use and expires after 10 minutes. Only a `/start <nonce>` claims owner, and the wizard waits with a live status line until it arrives.
- Runtime: grammY long polling, typing action during a turn, replies split at 4096 chars and sent as HTML.
- Media in: photos (vision), voice (transcription skill), small documents (text extracted).
- Inline keyboards for reminder actions, approvals and `/connectors`.

WhatsApp
- Connect: the wizard first asks for the owner's phone number. Then it pairs by phone-number code (8 chars, big Geist Mono, `ABCD-EFGH`, copy button), with QR as a fallback tab. Expiry is driven by Baileys `connection.update`: a new `qr` arrives on each rotation, and the pairing code is requested after the socket opens. A text label ("expires in about 2 min") isn't announced on every tick. Session files live in `/data/wa-auth`.
- Owner match: messages from the stored number are accepted. The LID is learned from the first DM whose `key.remoteJidAlt` (or the LID mapping) matches the number, and both are stored. Anything with `fromMe` is dropped.
- Number: utusan's number (D2), linked as its own extra linked device with its own auth (WhatsApp allows 4). Separate credentials means no 440 fight. Both devices receive every message, so chats are split, never shared: Palui answers only the chats in its own allowlist, and each chat moved to Palui gets its utusan binding removed in the same step (section 18). A wizard check refuses a chat that utusan still binds (it reads `~/.utusan/utusan.json` through a read-only mount, or I tick a box when it can't).
- Runtime: a lock on the auth dir, reconnect with exponential backoff, and 440 means "another session took over", so it pauses with that reason instead of fighting. A logout means `error: unlinked`.
- DM only. An ⏳ reaction while thinking, cleared on reply.
- Risk: Baileys is unofficial. Keep volume low, use one owner, never broadcast. Telegram is the reliable channel.

Web chat
- Lives in the dashboard. It goes through the same router and turn, streamed, with tool calls visible. It's for testing without a phone.

Dev vs prod
- Dev uses a separate Telegram bot token, since two `getUpdates` consumers on one token get 409. No WhatsApp in dev: the shared number already carries utusan and prod Palui. `pnpm dev` refuses to start a channel whose token matches prod.

### 5.2 Models

CLIProxyAPI
- Fields: base URL (default `http://host.docker.internal:8317`), API key, protocol per role (`anthropic` uses `/v1/messages`, `openai` uses `/v1/chat/completions`).
- Test: `GET /v1/models`, then a 1-token call per role. Shows the model list.
- Roles: `default` (turns), `fast` (titles, summaries, reminder parsing), `vision`.
- Failover per role: on 5xx, 429 or a quota error, try the next connector in the chain.
- Caching: with `routing.strategy: round-robin` across 4 Claude accounts, prompt caching mostly misses. D8 decides whether to change CLIProxyAPI's strategy (it's shared with other tools on the mini). M0 measures tokens per turn under both.

ai-gateway
- Same shape, `zkg_` key. The fallback in every chain.

Budget
- Per turn: 12 steps, 120 s wall clock, about 30 tools at most (the most relevant connectors first when there are more).
- Global: a daily token cap, a per-job daily cap, and a max of 60 runs per hour. Hitting a cap pauses turns and tells me on the channel.
- `/stop` aborts the running turn (`abortSignal`). `/status` shows the model, tokens today and connector states.

### 5.3 Memory: recall

- Fields: URL (default `http://host.docker.internal:4800`), API key (`rcl_`), container picked from a Select filled by `GET /v1/containers` (recall's default is `default`).
- Test: `GET /health`, then `GET /v1/containers`.
- Tools: recall's own `/mcp` endpoint, mounted as an MCP connector (`add_memory`, `search_memories`, `ask_memory`, `get_profile`, `list_memories`). Forget is `DELETE /v1/memories/:id`, one REST tool classed `destructive`.
- Auto-recall (Switch): `GET /v1/profile?container=…&q=<message>` before a turn, 1.5 s timeout, skip rate shown on the card. Skipped for commands and messages under 4 words.
- Auto-save (Switch): runs at session compaction and on `/new`, with `POST /v1/memories` sending my messages plus the final replies, never tool output. `source_id = <session>:<range>` keeps it idempotent. Tainted turns (section 10) are skipped.

### 5.4 Tools: MCP servers

- One connector per server, with URL and auth (none, bearer, custom header). Streamable HTTP only.
- Test lists the tools. Each tool gets its own Switch and a class (`read`, `write`, `destructive`). Unknown defaults to `destructive`. `readOnlyHint` and `destructiveHint` prefill the class.
- A trust flag per server: `own` (my servers) or `external`. Results from `external` servers taint the turn (section 10).
- Tools are namespaced `server__tool`.
- Presets for servers I already run (vorneo, keuangan, todomap, linear) ask only for the token.

### 5.5 Skills (built-in connectors)

- Reminders (section 6) and Jobs (section 7).
- Voice-in: ElevenLabs Scribe. Gemini audio through CLIProxyAPI is the fallback if the M0 spike shows it works.
- Web fetch: fetch + readability behind the SSRF guard. Results taint the turn.
- Inbound webhooks: `POST /api/hooks/:slug` with an HMAC secret per hook. It forwards the event to me as text. The "summarize" option runs with zero tools.
- Calendar: D7 decides whether a Google Calendar read connector is in v1.

## 6. Reminders

The feature most likely to be judged by its failures.

- Created by chat ("ingetin bayar listrik besok jam 9", "remind me every Monday 8am to send the report") or in the dashboard.
- Tools: `ingatkan` (create), `daftar_pengingat`, `ubah_pengingat`, `batal_pengingat`, `tunda_pengingat`.
- Time: the default zone is `Asia/Makassar` (WITA). Each reminder stores an IANA zone. Recurrence uses one grammar, RRULE, but the next occurrence is computed in wall-clock time with Luxon in the stored zone (not `rrule`'s TZID handling, which returns floating dates). `fire_at` is stored in UTC. The tool echoes the parsed time back ("Besok, Rabu 11 Nov, 09.00 WITA") so a wrong parse shows right away.
- Delivery: a due reminder becomes an outbox row, marked `sent` only after the channel acks. Retry with backoff for up to 24 h. After 2 failed attempts it falls back to the other channel (a setting).
- Downtime: on start, overdue reminders fire once with "(telat, harusnya 09.00)". A recurring reminder that missed several runs fires once.
- Actions: Done, and Snooze for 10m / 1h / tomorrow 09.00. Inline buttons on Telegram. On WhatsApp, reply "done" or "tunda 1j" quoting the reminder.
- Plain reminders never call the model. A "smart reminder" option runs a prompt at fire time.
- Scheduler: one timer armed for `min(fire_at - now, 60_000)`. Node clamps delays over about 24.8 days to 1 ms, so the cap avoids a tight loop. There's also a 30 s safety sweep. Both use the same idempotent claim: `UPDATE … SET status='firing' WHERE status='pending' AND fire_at<=now RETURNING`.
- Tests (part of M2's done-when):
  - Fake-clock unit tests: America/New_York DST gap and overlap, "last Friday", the 31st in a 30-day month, the restart-with-overdue case, the 25-day-out timer.
  - A parse eval: 60 Indonesian and English phrases against a fixed "now", at least 95% exact.

## 7. Scheduled jobs

- A job is an RRULE, a zone, a prompt, a target chat, a tool scope and a model role.
- Runs as a turn with `trigger = schedule`. History goes to `runs`.
- Jobs can't use `destructive` tools, and every `write` tool needs approval (nobody's watching).
- The daily briefing in v1 reads: reminders for today, recall profile, todomap tasks (MCP), vorneo pipeline (MCP), weather via web fetch. Calendar only if D7 is yes.
- Heartbeat preset: every 30 min from 08:00 to 22:00, reading reminders and MCP inboxes, and answering `NOTHING` when nothing matters (the runtime then sends nothing).
- Dashboard: list, next run, last result, Run now, Pause.

## 8. The agent turn

1. Inbox: every inbound message is written to `messages` with `status=pending` before the channel handler returns. Pending rows are replayed on start. grammY confirms updates when it fetches the next batch, and Baileys delivers once, so this is what keeps a message sent mid-deploy.
2. Router: identify the channel and sender. Non-owners are dropped, with the reason logged as a sentence. A message that answers a pending approval ("ya"/"batal", or a button callback) is matched here, before the model sees it. Commands (`/new`, `/stop`, `/status`, `/connectors`) never reach the model.
3. Per-chat queue: one turn at a time per chat. Messages that arrive mid-turn are batched into the next turn.
4. Context: persona, now (date and WITA), the list of enabled capabilities, recall profile, and the session (summary plus the last 30 messages).
5. Loop: AI SDK with tools from enabled connectors, within the section 5.2 budget.
6. Approvals: tools that need approval (`destructive` always, `write` in tainted turns and jobs) use AI SDK 7 tool approval. `generateText` returns `tool-approval-request` parts and the turn ends. The `approvalId` and response messages are saved to `pending_approvals`, the queue is freed, and I get a prompt (buttons on Telegram, "ya/batal" on WhatsApp). The answer resumes it with a new call carrying a `tool-approval-response`. After 10 minutes it resumes with a denial so the model can close the loop. This survives restarts.
7. Reply: written to the outbox, split and formatted per channel.
8. Bookkeeping: tokens, tool calls and duration go into `runs`, tagged with `run_id` in logs.
9. Compaction: past about 40 messages, the `fast` model writes a summary and older messages leave the context (they stay in the DB). Auto-save to recall runs here.

Delivery is at least once. Outbox rows stuck in `sending` for more than 5 minutes go back to `pending`.

## 9. Data model (SQLite, Drizzle)

| Table | Key columns |
|---|---|
| `connectors` | id, type, kind, label, enabled, status, status_detail, trust, config json, secrets (encrypted), config_hash, updated_at |
| `identities` | id, channel, external_id, alt_id (LID), display_name, role |
| `claims` | nonce, channel, expires_at, used_at |
| `chats` | id, channel, external_id, title, kind, session_id |
| `sessions` | id, chat_id, summary, saved_to_recall_at |
| `messages` | id, session_id, role, content json, channel_msg_id, status (pending/handled), created_at |
| `runs` | id, chat_id, trigger, model, tokens_in, tokens_out, tainted, status, error, started_at, ms |
| `tool_calls` | id, run_id, connector_id, tool, class, args json, result_preview, status, ms |
| `pending_approvals` | id, run_id, chat_id, approval_id, messages json, prompt_msg_id, expires_at, status |
| `reminders` | id, chat_id, text, rrule, tz, fire_at, status, last_fired_at, snoozed_until |
| `jobs` | id, name, rrule, tz, prompt, chat_id, model_role, tool_scope, enabled, next_run_at, daily_token_cap |
| `outbox` | id, chat_id, payload, status, attempts, next_attempt_at, claimed_at, channel_msg_id, source |
| `events` | id, at, level, connector_id, kind, message |
| `settings` | key, value |

All writes go through transactions, with no JSON state files. Retention: `tool_calls` args and results for 30 days, media for 14 days, `events` for 90 days.

## 10. Security

- Dashboard: passkeys, `disableSignUp: true` from the first boot. The owner comes from `docker compose exec app palui owner invite`, a single-use link valid for 15 minutes, which is also the recovery path for a lost passkey (recall's pattern). App routes are `noindex`.
- Channel owner: claimed with a Telegram nonce link and a WhatsApp phone number plus learned LID (section 5.1). Everyone else is silently dropped and logged.
- Taint rule: a turn becomes tainted once it reads untrusted content (web fetch, a hook payload, results from an `external` MCP server). For the rest of that turn, every `write` tool needs approval too, and auto-save skips it. A label in the prompt isn't counted as a control.
- recall auto-save stores only my messages and final replies, so tool output can't become a permanent injection through auto-recall.
- Jobs and hooks: no `destructive` tools, `write` needs approval, and hook summaries run with zero tools.
- Tool classes fail closed. Unknown means `destructive`.
- Secrets: encrypted, write-only, redacted in logs.
- Network: control API on the compose network only, `app` on 127.0.0.1, SSRF guard on every fetch the agent can steer.
- CLIProxyAPI logs: its config has `debug: true`, `logging-to-file: true` and `logs-max-total-size-mb: 0`, and the logs folder is already 154 MB. Prompts with recall memories would land there. D8 includes capping it.

## 11. Dashboard UI/UX

### 11.1 Design contract

- Audience: me, one technical owner, often on a phone between other things.
- Primary task: see that it's alive, and flip a connector in seconds.
- Category default: a chatbot SaaS dashboard (KPI cards, big charts, chat in the middle).
- Predictable opposite: a raw settings list or config editor.
- What this product shows that neither does: a live picture of what's plugged in. Health and control are the same element. Every connector shows on, paused or broken, and it's one tap from off.
- Required states per page: loading (Skeleton in the final layout), empty (Empty with the one next step), error (Alert with the cause and fix), long content (long MCP tool names, long chat titles, emoji in names), 390 px width.
- Finish counts: 1 accent hue, radius only from `--radius` steps, 0 emoji in chrome, 0 gradients, 0 inert controls.

### 11.2 Visual system

- Sibling of recall: Geist and Geist Mono, recall's oklch token file (`baseColor: neutral`), `--radius: 0.625rem`, the same `emphasized` and `drawer` easings.
- One accent hue, picked in Explore from colors that can't be mistaken for a status. Candidates: daun pisang (banana-leaf green, only if "on" uses a different green plus an icon), sogan (Javanese brown), Banjar sasirangan indigo. Kunyit amber is out because amber reads as paused.
- Status: on is green + check icon, paused is zinc + pause icon, error is red + alert icon, connecting is a Spinner. Always text plus icon, never color alone.
- Copy: English UI strings, with an i18n file from day one (`en`, `id`). The bot speaks casual Indonesian by default (D6).

### 11.3 Shell

shadcn `sidebar-07` (collapses to icons, becomes a Sheet on a phone). The header has the page title, a global status ("All on" or "2 need attention", as a link) and a Command menu (⌘K) for "turn off recall", "new reminder", "open chat".

### 11.4 Pages (7)

| Page | Content | Main components |
|---|---|---|
| Home | A first-run checklist until it's done (owner passkey, model, channel, say hi, optional recall and MCP). After that: a connector summary (the connector map from the Explore pick), the next 3 reminders, the last 5 runs, tokens today. | Card, Empty, Badge, Switch, Item, Skeleton |
| Chats | One page: the list of every chat across channels on the left (a Drawer on a phone), the transcript on the right. The web chat is the "Palui (web)" chat with an input. Tool calls are inline and collapsible, with a class badge and duration. Pending approvals show Approve/Deny. | Card, Collapsible, InputGroup, Badge, ScrollArea, Item. AI Elements only if `shadcn view` shows it works on base-nova |
| Reminders | Tabs for Upcoming, Recurring and Done. The New reminder dialog takes natural language and previews the parsed time live. | Tabs, Table, Dialog, Field, InputGroup, Calendar, Popover |
| Jobs | Schedule, next run, last result, Run now, Pause, daily cap. | Table, Switch, Sheet, Badge |
| Connectors | Connectors grouped as Channels, Models, Memory, Tools, Skills. Each card shows an icon, name, one line, status, a Switch and an "Open" button. The card itself isn't a button. Open shows a Sheet (a Drawer on a phone) with the setup wizard or config, Test, recent events and the danger area. "Add connector" opens a catalog in a Command. The MCP sheet has a tool table with a Switch and class Select per tool, search, and "enable all read tools". | Card, Switch, Button, Badge, Sheet, Drawer, Field, Alert, AlertDialog, Command, Table, Select |
| Activity | Tabs: Events (filter by connector and level), Runs, Usage (tokens per day, model, chat and job, with no dollar figures, since CLIProxyAPI has no usage stats and subscriptions have no per-token cost). | Tabs, Table, ToggleGroup, Badge, `chart-*` blocks |
| Settings | Persona, timezone, language, model roles and failover, budgets, passkeys, API keys, backup status. | Field, Textarea, Select, Tabs |

### 11.5 Interaction rules

- Pause never asks. Remove always asks, and the dialog names the consequence.
- The Switch has `aria-label="Pause Telegram"` (or Resume) and a 44 px touch target.
- Test, Connect and Run now use `toast.promise` (loading, then success with detail, or an error with the fix).
- The connect wizard keeps its progress if the sheet closes (a draft row with status `not set up`).
- Status is live over SSE from `events`. Changes are announced through `aria-live="polite"`, once per change, never per tick.
- Phone first for Home, Connectors, Reminders and Chats. Sheets become Vaul Drawers on narrow screens.

### 11.6 Motion (frequency test)

| Element | Frequency | Decision |
|---|---|---|
| Connector Switch | often | shadcn default thumb slide only |
| Status change | occasional | 150 ms color crossfade, no movement |
| Sheet / Drawer | often | shadcn default, `drawer` easing, interruptible, drag to dismiss on a phone |
| Home connector map | first visit only | lines draw once ever (a localStorage flag), then static. A busy connector shows a Spinner badge, with no pulses |
| Chat messages | very often | none for mine, 150 ms opacity when agent text starts streaming |
| Tables, lists | often | none |
| Toasts | occasional | Sonner default |
| Reduced motion | always | everything instant |

### 11.7 Explore (at build time)

Explore runs after M3, when 6 or more real connectors exist. Before that, the Connectors page is a plain shadcn grouped list. The piece is the Connectors page, with three variants behind the `prototype` picker:

1. A card grid grouped by kind with a Switch per card (closest to TASTE.md and shadcn).
2. Dense rows, Raycast or macOS settings style, with inline status and expandable details (density axis).
3. A wiring board: Palui in the middle and connectors as nodes you plug and unplug (spatial axis).

I pick one, and it's recorded in the repo's `DESIGN.md ## Decisions`. The Home map follows that pick.

## 12. Landing page and docs (Cloudflare Pages)

Follows the TASTE.md lines.

- Landing: a short headline, one sentence, one call to action ("Read the docs"), one strong visual. No feature grid, no screenshots.
- Hero visual (Explore at M6, 3 variants):
  - (a) Graph: a message travels from a WhatsApp or Telegram node into Palui, out to recall, reminders, MCP and model nodes, and back.
  - (b) Chat replay: "ingetin bayar listrik besok jam 9", then the reminder firing the next morning.
  - (c) connector board: connectors switching on one by one.
  - Every variant gets an icon on each node, starts from step 1 on load, types its caption at about 55 ms per character with no caret, moves the visual only after the line is written, and keeps the visual and caption in one phone screen with the caption first.
- Positioning while dev-only: "Personal agent, self-hosted." The landing is indexable (D4), `/app` is not.
- Ship: OG image, favicon set, `robots.txt`, sitemap, per SHIP.md. Optionally, a small Si Palui-inspired mark (original drawing, not the comic's art).
- Docs (Fumadocs):
  - Getting started: install, first owner, first connector, first message.
  - Concepts: connector states, channels, chats and sessions, memory, reminders, jobs, tool classes, approvals, the taint rule.
  - One page per connector. Its skeleton is generated from the manifest.
  - Chat commands reference (`/connectors`, `/new`, `/stop`, `/status`).
  - REST API.
  - Self-hosting: compose, volumes, backups and restore, upgrades, tunnel.
  - Security model.
  - Changelog.
- Public copy rule: no secrets, home paths, internal hostnames or IPs.

## 13. Deploy and operations

- Compose as in section 3.1. `.env` keys, which I create: `MASTER_KEY`, `BETTER_AUTH_SECRET`, `APP_URL`. A copy of `MASTER_KEY` also goes into my password manager. Every other credential is encrypted in the DB.
- `scripts/pre-deploy.sh`: snapshot the DB (copied from recall), keep 3.
- `/health` fails when the DB schema is newer than the code, so a bad rollback shows as failed.
- Nightly backup to R2: DB snapshot plus `wa-auth`, encrypted with age, with the key in Keychain. `wa-auth` alone can take over the bot's number.
- Restore drill (part of M2): restore the latest R2 snapshot into a scratch compose project, check that `/health` is green and that one secret decrypts.
- Auto-deploy: add the repo to `webhook/apps.json` and create the GitHub hook (pre-approved).
- m4-alerts: add the public `/health`. The runtime also messages me when a connector stays in `error` for 5 min, with Resend email as the fallback when no channel is up.
- Disk: the mini had 4.1 GB free on 2026-11-09, and `deploy.sh` refuses under 5 GB. Free space in M0.

## 14. Milestones

Each one ends deployed on the mini and usable.

| # | Scope | Done when | Estimate |
|---|---|---|---|
| M0 | Free disk to >10 GB. Spike: AI SDK tool calling through CLIProxyAPI for a Claude model (anthropic protocol) and a Gemini model, from an OrbStack container, with tokens per turn measured under round-robin vs sticky. Spike: Baileys pairing in a Linux container. Check the compose single build plus `pull_policy: never`. | Each model passes 5 tool-call round trips. Pairing works. Compose builds once. | ~1 day |
| M1 | Monorepo, DB, CLI owner invite and passkey, connector framework (manifest/runtime split) and supervisor, CLIProxyAPI + ai-gateway connectors with failover and budgets, web chat in Chats, plaon the Connectors page list, deploy + tunnel + edge | I chat at `palui.zakiego.com/app`, pause the model and get the fixed reply, and a deploy doesn't lose a message | ~5 days |
| M2 | Telegram (nonce claim), inbox, outbox, reminders, `/connectors` `/stop` `/status`, scheduler tests, parse eval, encrypted backup + restore drill | A reminder set from Telegram fires after a deploy mid-wait, Snooze works, eval ≥95%, restore drill passes | ~4 days |
| M3 | recall (MCP + auto-recall/save), MCP connectors with trust and classes, approvals that survive restarts, taint rule | A fact saved yesterday is recalled, a vorneo tool runs, a destructive tool asks first and still works after a restart | ~3 days |
| M3.5 | Connectors page Explore (3 variants), the pick applied to Connectors and Home | Pick recorded in DESIGN.md | ~1.5 days |
| M4 | WhatsApp (phone number owner, LID learning, pairing UI) | Pair, chat, a reminder delivered on WhatsApp, pause/resume without re-pairing | ~2.5 days |
| M5 | Jobs, heartbeat, voice-in, media, Activity page, inbound hooks | The daily briefing arrives at 07:00 for 3 days straight with real content | ~2 days |
| M6 | Landing + docs on Pages (can start after M1) | Lighthouse SEO 100, OG renders in a debugger, docs cover every v1 connector | ~2.5 days |

About 21 days in total. Every UI milestone runs the full zaki-design-ui pipeline, which is included in the estimates.

## 15. Decisions I need from you

| # | Question | Recommendation |
|---|---|---|
| D1 | Name | Decided: Palui Agent (`palui`) |
| D2 | WhatsApp number | Decided: utusan's number, as a separate linked device, chats split per section 18. utusan will be sunset |
| D3 | Replace utusan? | Decided: yes, someday. Roadmap in section 18 |
| D4 | Landing indexable now? | Decided: yes |
| D5 | Open source? | Decided: private |
| D6 | Bot's default language | Decided: casual Indonesian, English when I write English |
| D7 | Google Calendar read connector in v1? | Decided: yes, read-only, in M5 |
| D8 | CLIProxyAPI shared config | Decided (Claude's call): log cap set to 200 MB on 2026-11-10 (backup `config.yaml.bak-palui-logcap`). `debug: true` left as is. Routing stays round-robin unless M0 shows sticky routing saves a lot of tokens |

## 16. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Tool calling through CLIProxyAPI is flaky for some models | Medium | M0 spike, anthropic protocol for Claude, ai-gateway failover |
| Subscription quota burn from loops or jobs | Medium | Budgets and caps in M1, `/stop`, cache-friendly routing (D8) |
| Prompt injection through recall, MCP, fetch or hooks | Medium | Taint rule, approvals, auto-save without tool output |
| Baileys ban or protocol break | Low/Medium | Low volume, Telegram primary, pinned version. A ban now hits utusan and Palui together, which is one more reason to keep Palui's WhatsApp traffic small until utusan is gone |
| Both bots answer the same chat | Medium | One owner per chat, enforced by the wizard check and the move step in section 18. `fromMe` dropped, so neither bot answers the other |
| Disk on the mini | High | Free space in M0, retention, capped logs |
| Deploy drops a message or reminder | Medium | Durable inbox, outbox, drain on SIGTERM, liveness-only runtime health |
| Scope creep toward OpenClaw parity | High | The parity table. Every new feature must be a connector |

## 17. Testing and observability

- Unit (`node --test` or vitest): scheduler and recurrence (fake clock), outbox state machine, router ownership including JID vs LID, connector reconcile and the section 4.3 rules, the taint rule, budget caps.
- Integration: grammY with a test transport, Baileys behind a mocked socket, MCP against a local test server, AI SDK with a mock model for approvals and resume.
- Evals: the reminder parse set (60 phrases), plus a small injection set (a poisoned fetch result tries a write tool and must ask for approval).
- Post-deploy smoke: a script sends a Telegram message from a test account to the dev bot and expects a reply. On prod, `/health` plus the m4-alerts probe.
- Logs: JSON lines tagged with `run_id`, `chat_id` and `connector_id`, with secrets redacted, capped per zaki-selfhost.

## 18. Roadmap: sunsetting utusan

Palui takes over utusan's number one chat at a time, then utusan is switched off. Same idea as utusan's ADR 0004: a parity checklist, then a parallel run.

Moving a chat (Palui CLI `palui wa move <chat>`, and later a button):
1. Add the chat to Palui's WhatsApp allowlist, paused.
2. Remove the chat's binding from `~/.utusan/utusan.json` (hot reloaded, no restart).
3. Turn the chat on in Palui. Send a test message.
Undo is the same steps reversed.

Order:

| Phase | Chats | What Palui needs first |
|---|---|---|
| R1 (with M4) | My own DM with the bot | v1 WhatsApp |
| R2 | My other personal chats (jurnal "15 minutes" group, code-notif) | A Jurnal skill (the 15-min ask, SQLite store, nightly recap that replaces the cloop `jurnal-ringkasan` job), plain notification sends for selfhost and m4-alerts |
| R3 | Project groups (Vorneo and others) | Groups with mention-only replies, per-chat roles like utusan's IAM (guest/member/admin/owner), per-chat memory (recall container per group), per-chat persona overlays |
| R4 | `vorneo.id/admin/chat` web bridge | A web channel with HMAC tickets (utusan's `src/lib/ticket.ts` design) |
| R5 | Switch off utusan | Every item in utusan's skill and tool list has a Palui owner or is dropped on purpose: gws, skill reading, `kirim_file`, voice notes, `/approve` stub flow, dashboard. One week with utusan stopped but restorable (`launchctl bootout`, keep `~/.utusan`) |

After R5: archive the utusan repo, remove its launchd plist, update the `utusan`, `notify-wa`, `jurnal-ringkasan` and `email-watcher` skills to send through Palui (a small `palui send` CLI or REST endpoint, added in R2).
