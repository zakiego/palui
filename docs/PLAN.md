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
| Persona file (SOUL.md) | yes | yes, one per agent (section 19) |
| Cron | yes | yes, Jobs |
| Heartbeat | OpenClaw | yes, as a Job every 30 min with a "say nothing if nothing matters" prompt |
| Model failover | yes | yes, per model role |
| Approvals for risky tools | yes | yes, saved and resumable (section 8) |
| Self-written skills | Hermes | deferred |
| Subagents | both | deferred |
| Group chats | both | yes, with an agent, mention or reply only (section 19) |
| Voice replies (TTS) | both | deferred. Voice-in only |
| Multiple profiles or agents | both | yes, section 19 |
| stdio MCP, shell, browser | both | deferred (needs binaries in the container and a sandbox). Host commands work today through `apps/host-bridge`, an allowlist MCP server on the host |

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
- Number: Palui's own number (D2), not utusan's. Nothing is shared with utusan, so there's no chat split and no utusan check. Every DM from the owner is answered. Groups and other people's DMs start outside the allowlist until they get an agent (section 19).
- Runtime: a lock on the auth dir, reconnect with exponential backoff, and 440 means "another session took over", so it pauses with that reason instead of fighting. A logout means `error: unlinked`.
- DMs, plus groups with an agent (section 19). An ⏳ reaction while thinking, cleared on reply.
- Risk: Baileys is unofficial. Keep volume low, use one owner, never broadcast. Telegram is the reliable channel.

Web chat
- Lives in the dashboard. It goes through the same router and turn, streamed, with tool calls visible. It's for testing without a phone.

Dev vs prod
- Dev uses a separate Telegram bot token, since two `getUpdates` consumers on one token get 409. No WhatsApp in dev: the number is already linked to prod Palui. `pnpm dev` refuses to start a channel whose token matches prod.

### 5.2 Models

CLIProxyAPI
- Fields: base URL (default `http://host.docker.internal:8317`), API key, protocol per role (`anthropic` uses `/v1/messages`, `openai` uses `/v1/chat/completions`).
- Test: `GET /v1/models`, then a 1-token call per role. Shows the model list.
- Roles: `default` (turns), `fast` (titles, summaries, reminder parsing), `vision`.
- Failover per role: on 5xx, 429 or a quota error, try the next connector in the chain.
- Caching: with `routing.strategy: round-robin` across 4 Claude accounts, prompt caching mostly misses. D8 decides whether to change CLIProxyAPI's strategy (it's shared with other tools on the mini). M0 measures tokens per turn under both.
- Prompt cache layout: a provider reuses a prompt only up to its first changed byte, so a turn is built stable first. The system prompt holds no clock, recall or check-ins and repeats byte for byte between turns; the session summary is a second system block; the history window drops its oldest messages ten at a time. What changes every turn (now, open check-ins, recall) rides in a `[Palui context]` block at the end of the newest user message and is not stored. On the anthropic protocol three breakpoints are set: the stable system block and the newest user message (1 hour, since chat messages are often more than 5 minutes apart and a read restarts the clock), and the last tool result from a turn's second step on (5 minutes, it is only read seconds later). The openai protocol gets the same layout and no breakpoints. CLIProxyAPI moves the caller's system prompt behind the first user message, so the second turn of a new session misses once.

ai-gateway
- Same shape, `zkg_` key. The fallback in every chain.

Budget
- Per turn: 12 steps, 120 s wall clock, about 64 tools loaded up front. Past that, tool search takes over: tools loaded earlier in the session go first, then skills, memory and the rest, one slot goes to `palui__cari_tool`, and every other enabled tool is named in the capability list and loaded on demand by keyword or exact name. Loaded tools join the active set from the next step and stay for the session (`sessions.loaded_tools`, 20 most recent).
- Global: a daily token cap, a per-job daily cap, and a max of 60 runs per hour. Hitting a cap pauses turns and tells me on the channel.
- Tokens read from the prompt cache count at 10% against the global, agent, job and flow caps, about their price next to fresh input. Fresh input, cache writes and output count in full.
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

### 5.4.1 Tools: T3 Code

- Connects to the T3 Code server on the Mac (`http://host.docker.internal:3773` from Docker) over its `/mcp` endpoint.
- Setup takes a pairing code (`t3 auth pairing create`). The runtime trades it through T3 Code's OAuth endpoints (register, pairing-code decision, token) for a 30-day full-access token and stores it encrypted with `patchSecrets`. T3 Code has no refresh grant, so an expired token is a connector `error` and a new code fixes it.
- Tools: `daftar_proyek`, `daftar_thread`, `thread_chat_ini`, `lihat_thread` (read), `mulai_thread`, `kirim_ke_thread` (write), `hentikan_thread` (destructive). New threads get their own worktree off a default base branch.
- A watcher polls the runs Palui started every 20 s and messages the chat they came from when one finishes (the agent's reply, clipped, plus the link), or once when it waits for an answer. Watches live in the connector's config and expire after 24 h.

### 5.5 Skills (built-in connectors)

- Reminders (section 6) and Jobs (section 7).
- Voice-in: ElevenLabs Scribe. Gemini audio through CLIProxyAPI is the fallback if the M0 spike shows it works.
- Web fetch: fetch + readability behind the SSRF guard. Results taint the turn.
- Inbound webhooks: `POST /api/hooks/:slug` with an HMAC secret per hook. It forwards the event to me as text. The "summarize" option runs with zero tools.
- Calendar: D7 decides whether a Google Calendar read connector is in v1.
- Attachments: `simpan_lampiran`, `daftar_lampiran`, `ambil_lampiran`, `hapus_lampiran`. The model keeps only files that matter (ID cards, receipts, tickets) from the message being answered. Allowlisted types, 10 MB cap, stored encrypted with MASTER_KEY in `saved_attachments`, listed and downloaded (signed in only) on the Attachments page.

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

## 6b. Routines (removed)

Routines were the first generic automation (a time source, one action, a recap). Flows (6c) replaced them: a routine is a flow with one schedule trigger and one step. The routine engine, the `routines` skill, the Routines page, the `routineMode` setting, the `palui flows status|mode` commands and the `rt:` buttons are gone. The app deletes the retired `routines` connector row when it opens the database. The `routines` and `routine_occurrences` tables and the `legacy_*` and `shadow` columns on the flow tables stay for one release so a rollback still finds them; a later migration drops them.

## 6c. Flows

Automations the agent builds from chat out of triggers and steps (`packages/core/src/flows`, tools in the `flows` skill). Accepted from `docs/rfc/flows.md`, which keeps the reasoning and the rejected alternatives; this section is the source of truth for behavior. Flows replaced routines (6b).

- Definition: JSON `{ schema, name, description?, tz?, triggers[], steps[], limits? }`, validated by zod (`flows/schema.ts`) and by the graph and data checks in `flows/validate.ts`: unique literal ids, known branch targets, no cycles, nothing unreachable, at most 40 steps, 8 triggers, 32 KB. `schema` versions the format; `upgradeDefinition()` reads old shapes, so a format change needs no data migration.
- Triggers: `schedule` (fixed `times`, an `rrule`, or times read daily from a read `tool`, `flows/source-config.ts`), `webhook` (a hook in mode `flow`, payload always tainted), `chat` (an owner command or exact phrase in the flow's chat, matched by the router before the model; built-in commands are refused), `poll` (a read tool every 5 to 1440 minutes; the first poll only seeds; fires on change or on a new non-empty value), `event` (`checkin.answered`, `checkin.missed`, `run.done`, `run.failed` from a named flow or `self`; chain depth over 3 is skipped; never your own `run.failed`), and manual (`jalankan_flow`). Each fires at most once per run key.
- Steps: `tool`, `if`, `set` (run or flow scope, flow state capped at 16 KB), `message` (optional literal `to` an owner or allowed chat), `checkin` (answers, re-asks, deadline `next` or `after`, `on.<answer>` and `onMissed` branches), `wait` (up to 7 days), `prompt` (a literal instruction, data in `<data>`/`<untrusted>` blocks, tools allowlisted, `"*"` for every tool under unattended rules), `each` (one child run per item, key-deduped across runs as `each:<step>:<key>`, 20 by default and 50 at most, no nesting). Every step has `next`, `onError` (`fail`, `continue` or a step id) and `timeoutSec`.
- Data: templates `{{path | filter}}` only in tool args, message and check-in text, prompt input, set values, `wait.until` and `each.key`; ids, tool names, branch targets, `message.to` and the prompt instruction are literals. Expressions (`flows/expr.ts`) are parsed at save into an AST: and/or/not, comparisons, arithmetic (null counts as 0 in + and -), a fixed function list; no eval and no regex from the flow, bounded to 200 nodes.
- Taint: per run, a set of roots (`trigger`, `steps.<id>`, `vars.<name>`, `flow.<name>`, `item`). Untrusted tools, webhook bodies, untrusted poll or source tools and tainted prompt turns taint their root; `set` and `each` carry it. Message values are sanitized (control characters out, sent as plain text); tainted ones also lose line breaks and are capped at 500 characters. Conditions read tainted values freely.
- Tool calls: server-side through `connectorFlowCaller`, which resolves the class from the connector's tool list before calling and passes the flow's chat and id in the tool context. Destructive tools are refused at save and at run time. Read tools retry twice (30 s, 2 min).
- Approvals: write calls (and `message.to` an allowed chat) are listed per version (`flow_versions.write_calls`) and approved once per version with `flowver:approve|deny:<versionId>`; an edit with the same write calls keeps the approval. Until then, runs stop at the write step as `awaiting_approval` / `not_approved` and continue on approval. A single run asks again through `pending_approvals` (10 minutes) when an argument is tainted, the tool's class or connector type changed since the approval, or the tool's `flowApproval` is `every_run` (per tool on the MCP tool list, or set by a connector on its tools, read at run time).
- Runtime: the flow engine is a scheduler extension (resume, refresh, fire, polls, wake, execute, recover, daily prune). Every wait (delay, check-in, approval, retry) is a run row with `wake_at`; a step finishes in one transaction with its outbox row and cursor move. After a crash a read step runs again, a write step never repeats (`unknown outcome after restart`, the owner is told, `onError` decides), and a prompt step takes the finished turn's output. Limits: 30 runs per flow per hour, 120 in total, 3 running per flow, 100 step executions and 5 messages per run (re-asks and missed notes don't count), 50k tokens per flow per day (`limits.dailyTokens` up to 500k), runs live 8 days. Five failed runs in a row pause the flow; one failure notice per flow per day.
- Observability: every firing is a `flow_runs` row, skips included with a reason; every step attempt is a `flow_step_runs` row with its input, output, branch (an `if` keeps the values it compared), timing and error. `kenapa_flow` walks a fixed checklist for a flow and a moment. Retention: step inputs and outputs 30 days, runs 180 days, dry runs 30 days.
- Dry runs (`uji_flow`): read tools for real; writes, messages, check-ins and prompts stubbed (overridable per step); waits skipped; an each runs 3 items. Only a `dry_run` row and its step log are written.
- Tools: `buat_flow`, `ubah_flow` (a full definition or a merge patch, `steps`/`triggers` patchable by id), `cek_flow`, `uji_flow`, `daftar_flow`, `lihat_flow`, `jeda_flow`, `hapus_flow`, `jalankan_flow`, `riwayat_flow`, `lihat_run`, `kenapa_flow`, `jawab_checkin`, `batal_run`, and `flows__ringkasan` (the recap as data, usable as a step). Offered only in owner turns in the owner's DMs and the web chat. `palui__cari_tool` hits carry `class` and `untrusted`, and leave destructive tools out with `for: "flow"`.
- Dashboard: a read-only Flows page and a flow page (triggers, steps with branches, write calls, versions with their diffs, recent runs); a flow run opens its step log on the run page.

## 7. Scheduled jobs

- A job is an RRULE, a zone, a prompt, a target chat, a tool scope and a model role.
- Runs as a turn with `trigger = schedule`. History goes to `runs`.
- Jobs can't use `destructive` tools, and every `write` tool needs approval (nobody's watching).
- The daily briefing in v1 reads: reminders for today, recall profile, todomap tasks (MCP), vorneo pipeline (MCP), weather via web fetch. Calendar only if D7 is yes.
- Heartbeat preset: every 30 min from 08:00 to 22:00, reading reminders and MCP inboxes, and answering `NOTHING` when nothing matters (the runtime then sends nothing).
- Dashboard: list, next run, last result, Run now, Pause.

## 8. The agent turn

1. Inbox: every inbound message is written to `messages` with `status=pending` before the channel handler returns. Pending rows are replayed on start. grammY confirms updates when it fetches the next batch, and Baileys delivers once, so this is what keeps a message sent mid-deploy.
2. Router: identify the channel and sender. Non-owners are dropped, with the reason logged as a sentence, unless the chat is allowed and has an agent (section 19). A message that answers a pending approval ("ya"/"batal", or a button callback) is matched here, before the model sees it. Commands never start a turn: `/help`, `/new`, `/stop`, `/status`, `/usage`, `/connectors`, `/agent`, `/model`, `/models`, `/tools`, `/id`, `/report`, `/compact` and `/btw`. Only the last two call a model: `/compact` runs step 9 now (keeping the last 6 messages) in the chat's queue, and `/btw <question>` asks the `default` model one side question with no tools, the session as context, and nothing written back to the session. `/model <name>` edits the global `modelRoles` setting (the model goes first in the role's chain on every running connector that offers it). `/id` returns the chat, session and message ids, and those of a quoted message (looked up by its channel id in `messages`, then `outbox`). Slow commands (`/btw`, `/models`, `/model <name>`, `/tools`) answer off the routing chain, so `/stop` still lands while they work. `/report [note]` builds a plain-text debug snapshot of the chat for a coding agent (ids, recent runs and their errors, the last 10 messages, unsent outbox rows, warnings that name them), saves it in `reports` under an `rpt_` id and sends it. From a shared chat it goes to the owner's DM and the chat only gets the id (19.4).
3. Per-chat queue: one turn at a time per chat. Messages that arrive mid-turn are batched into the next turn.
4. Context: persona, now (date and WITA), the list of enabled capabilities, recall profile, and the session (summary plus the last 30 messages). The tool calls those turns made are replayed with them as real tool-call and tool-result parts (the result is the stored 500-character preview), so the model can tell what it ran from what it only said. Without that it distrusts its own replies and re-checks old work on every turn. A message that replies to another carries the quoted message in a `[Replying to ...]` block with `> ` lines, taken from the stored row or outbox when there is one, otherwise from the channel (WhatsApp `contextInfo.quotedMessage`). It is context only, never instructions. A quote of someone other than the owner or Palui taints the turn.
5. Loop: AI SDK with tools from enabled connectors, within the section 5.2 budget.
6. Approvals: tools that need approval (`destructive` always, `write` in tainted turns and jobs) use AI SDK 7 tool approval. `generateText` returns `tool-approval-request` parts and the turn ends. The `approvalId` and response messages are saved to `pending_approvals`, the queue is freed, and I get a prompt (buttons on Telegram, "ya/batal" on WhatsApp). The answer resumes it with a new call carrying a `tool-approval-response`. The AI SDK ran the other calls of that step when it asked, so on an approved resume their results are marked as taken before the approved call ran (the model calls again for the current state). After 10 minutes it resumes with a denial so the model can close the loop. This survives restarts. When the same call (same tool, same arguments) is made again in that chat while a request for it still waits, the older request is dropped without a follow-up: only the newest prompt is live.
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
| `agents` | id, slug, name, persona, is_default, recall_container, owner_powers, guest_tools, daily_token_cap, telegram_connector_id, avatar_image, avatar_icon, avatar_color |
| `chats` | id, channel, connector_id, external_id, title, kind, session_id, allowed, agent_id |
| `sessions` | id, chat_id, summary, saved_to_recall_at |
| `messages` | id, session_id, role, content json, channel_msg_id, sender_name, from_owner, status (pending/handled), created_at |
| `runs` | id, chat_id, agent_id, trigger, model, tokens_in, tokens_out, tokens_cached, tokens_cache_write, tainted, status, error, started_at, ms |
| `tool_calls` | id, run_id, connector_id, tool, class, args json, result_preview, status, ms |
| `pending_approvals` | id, run_id, chat_id, ask_chat_id, approval_id, messages json, prompt_msg_id, expires_at, status |
| `reminders` | id, chat_id, text, rrule, tz, fire_at, status, last_fired_at, snoozed_until |
| `jobs` | id, name, rrule, tz, prompt, chat_id, model_role, tool_scope, enabled, next_run_at, daily_token_cap |
| `routines` (retired, unused, dropped in a later migration) | id, chat_id, name, tz, days, source json, action json, recap json, enabled, paused_until, status, next_fire_at, next_occ, cursor, source_state, next_refresh_at, next_recap_at, last_error |
| `routine_occurrences` (retired, unused) | id, routine_id, chat_id, key (unique per routine), label, scheduled_at, status, answer, answers json, follow_ups, next_follow_up_at, deadline_at, fallback |
| `flows` | id, chat_id, name (unique per chat), tz, enabled, paused_until, current_version_id, approved_version_id, state json, consecutive_failures, agent_id, legacy_routine_id and shadow (both retired, unused), last_error, notice_for |
| `flow_versions` | id, flow_id, n, definition json, hash, write_calls json, created_by, note, approved_at, approved_via (immutable) |
| `flow_triggers` | id, flow_id, trigger_id, kind, match, status, next_fire_at, next_occ, cursor, source_state, next_refresh_at, poll_state, last_error |
| `flow_runs` | id, flow_id, version_id, chat_id, trigger_id, trigger_kind, run_key (unique per flow), parent_run_id, item_index, status, skip_reason, trigger json, vars json, taint json, cursor, wait json, wake_at, lease_until, steps_done, messages_sent, scheduled_at, started_at, ended_at, error, legacy_occurrence_id (retired, unused), dry_run |
| `flow_step_runs` | id, run_id, seq, step_id, kind, attempt, status, input, output, tainted, branch, tool_call_id, turn_run_id, error, started_at, ms |
| `outbox` | id, chat_id, connector_id, payload, status, attempts, next_attempt_at, claimed_at, channel_msg_id, source |
| `events` | id, at, level, connector_id, kind, message |
| `reports` | id, chat_id, session_id, note, text, created_at (one row per `/report`, kept until deleted) |
| `settings` | key, value |

All writes go through transactions, with no JSON state files. Retention: `tool_calls` args and results for 30 days, media for 14 days, `events` for 90 days.

## 10. Security

- Dashboard: passkeys, `disableSignUp: true` from the first boot. The owner comes from `docker compose exec app palui owner invite`, a single-use link valid for 15 minutes, which is also the recovery path for a lost passkey (recall's pattern). App routes are `noindex`.
- Channel owner: claimed with a Telegram nonce link and a WhatsApp phone number plus learned LID (section 5.1). Everyone else is silently dropped and logged, except in chats the owner allowed and gave an agent (section 19).
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
| Agents | Each agent's persona, instructions, shared-chat rules, daily cap and Telegram bot (section 19.7). | Table, Sheet, Field, Textarea, Select, Switch |
| Settings | Timezone, language, model roles and failover, budgets, passkeys, API keys, backup status. | Field, Textarea, Select, Tabs |

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
  - Chat commands reference (every command in section 8 step 2).
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
| D2 | WhatsApp number | Decided (changed 2026-11-11): Palui's own number. It used to be utusan's number with chats split, which is gone |
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
| Baileys ban or protocol break | Low/Medium | Low volume, Telegram primary, pinned version |
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

Palui runs on its own WhatsApp number, so it never shares a chat with utusan. Replacing utusan means moving each use over to Palui's number (my DM, then groups, then notification sends), then switching utusan off. Same idea as utusan's ADR 0004: a parity checklist, then a parallel run.

Order:

| Phase | Chats | What Palui needs first |
|---|---|---|
| R1 (with M4) | My own DM with the bot | v1 WhatsApp |
| R2 | My other personal chats (jurnal "15 minutes" group, code-notif) | A Jurnal skill (the 15-min ask, SQLite store, nightly recap that replaces the cloop `jurnal-ringkasan` job), plain notification sends for selfhost and m4-alerts |
| R3 | Project groups (Vorneo and others) | Agents in groups with mention-only replies and their own memory (section 19). Still open: per-chat roles like utusan's IAM (guest/member/admin/owner) |
| R4 | `vorneo.id/admin/chat` web bridge | A web channel with HMAC tickets (utusan's `src/lib/ticket.ts` design) |
| R5 | Switch off utusan | Every item in utusan's skill and tool list has a Palui owner or is dropped on purpose: gws, skill reading, `kirim_file`, voice notes, `/approve` stub flow, dashboard. One week with utusan stopped but restorable (`launchctl bootout`, keep `~/.utusan`) |

After R5: archive the utusan repo, remove its launchd plist, update the `utusan`, `notify-wa`, `jurnal-ringkasan` and `email-watcher` skills to send through Palui (a small `palui send` CLI or REST endpoint, added in R2).

## 19. Agents

Palui stops being one persona for one person. An agent is a named persona with its own standing instructions, its own recall container and its own daily token cap. Agents can be put in groups and in other people's chats, the way @grok answers in any thread it's tagged in. The owner still runs everything from the dashboard.

### 19.1 What an agent is

| Field | Meaning |
|---|---|
| `slug` | Lowercase id used in `/agent <slug>` and on the Agents page (`chef`, `vorneo-cs`). Fixed after creation. |
| `name`, `persona` | Display name and the persona text that opens the system prompt (max 8000 chars). |
| `recall_container` | Where its memory lives. Palui (the default agent) uses the recall connector's container. Every other agent gets `agent-<slug>`. |
| `owner_powers` | `off` or `approve`: what the owner's own messages get in this agent's shared chats (19.4). |
| `guest_tools` | `none` or `safe`: what other people can make it use (19.4). |
| `daily_token_cap` | Tokens per day across all its chats. 0 means only the global cap applies. |
| `telegram_connector_id` | Optional: its own Telegram bot (19.6). |

Standing instructions belong to an agent (`instructions.agent_id`, null means Palui). Preferences stay the owner's and only Palui reads them, since they hold personal data (`home_lat`, `default_city`).

The first boot after the migration creates Palui as the default agent from `settings.persona` and the existing instructions. Palui can't be deleted. It's the owner's personal agent, so it never goes into a shared chat: its memory is the owner's memory.

### 19.2 Chats

- Every chat has at most one agent (`chats.agent_id`, null means Palui). One agent can be in many chats.
- A chat is shared when it's a group, or a DM with someone who isn't the owner. The owner's DMs and the web chat are private.
- Shared chats need two things before anyone but the owner is answered: the chat is allowed, and it has an agent other than Palui. Both are set from the Chats page or with `/agent <slug>` sent by the owner in that chat.
- A message from a stranger in a chat that isn't set up is dropped and logged, but the chat row is created (allowed off), so it shows up on the Chats page to be set up.
- Groups: an agent replies only when it's @mentioned or when someone replies to one of its messages. Everything else in the group is dropped unread and never stored. A group where the owner never set an agent keeps today's rule: owner messages only.
- `/agent` lists agents. `/agent <slug>` assigns one and allows the chat. `/agent off` goes back to Palui (and stops answering guests). A switch saves the session to the old agent's memory and starts a new one, like `/new`. Only the owner can send commands. From anyone else, text that looks like a command is just text.

### 19.3 Who the model sees

- Every inbound message records `sender_name` and `from_owner`.
- In a shared chat each user message in the history starts with `[Name]:`, and the owner's start with `[Owner]:`. The system prompt says it's a shared chat, that only `[Owner]` lines come from the owner, and that the owner's private details aren't to be shared.
- Approval answers, reminder buttons and commands are owner only. A guest's "ya" is a normal message.

### 19.4 Tools and approvals

- Guest turn: any message in the batch is from someone other than the owner. It gets no tools (`guest_tools: none`) or only tools marked `guestSafe` in the connector contract (`guest_tools: safe`). Unmarked tools are never guest safe (fail closed). Today that's web fetch (`ambil_halaman`).
- Owner turn in a shared chat: with `owner_powers: off` it's treated like a guest turn. With `approve` it gets every tool, and every write or destructive call needs approval.
- Guest and shared-chat turns are strict: every `write` needs approval, like jobs.
- Approval prompts from a shared chat never go to that chat. They go to the owner's DM on the same channel (the web chat when there's none), and they name the chat they came from. `pending_approvals.ask_chat_id` is where the prompt went, and only an answer from there counts.

### 19.5 Memory and budget

- Auto-recall and auto-save use the agent's container. Palui keeps the owner's container.
- Auto-save writes owner lines as `Owner:`, guest lines as `<name> (in <chat>):` and replies as `<agent name>:`. Guest text is saved on purpose, so a group agent remembers people. That also means anyone in an allowed chat can put text into that agent's memory, never into Palui's. Turns tainted by an untrusted tool result are still skipped.
- A guest message makes the turn strict (19.4), but it doesn't mark the run tainted, so auto-save keeps it.
- `daily_token_cap` is counted in `usage` under `agent:<id>:<day>`. When it's hit, that agent stops answering until midnight (settings zone). The chat gets one notice per agent per day, and the global cap still applies on top.

### 19.6 Channels

- WhatsApp: one number for every agent. Groups (`@g.us`) are now read. The sender is `key.participant`. A message is addressed when `contextInfo.mentionedJid` holds Palui's number or LID, or when it quotes one of Palui's messages. Media from guests isn't downloaded. The model gets a note instead.
- Telegram: an agent can have its own bot. The Telegram connector allows several instances (`telegram`, `telegram-<slug>`). Chats are keyed by `(channel, connector_id, external_id)`, because two bots see the same DM and group ids. Replies go out through the bot the chat came in on (`outbox.connector_id`). In groups, a message is addressed when it mentions `@<bot username>` or replies to the bot. Bots in groups need privacy mode left on.
- The owner claim stays one per channel: claiming one bot makes that Telegram account the owner for every bot.

### 19.7 Dashboard

- Agents page: list with avatar, name, chats, tokens today against the cap. A sheet to create or edit: avatar, name, slug, persona, owner powers, guest tools, daily cap, recall container, Telegram bot, and a link to the agent's instructions on Notes. Palui's sheet hides the shared-chat fields.
- Avatar: an uploaded photo (cropped square and re-encoded as a 256 px WebP in the browser, stored as a data URL of at most 96 KB), else one of 24 icons on one of 9 colors, else Palui's mark for the default agent and a plain bot for the rest. Columns `avatar_image`, `avatar_icon`, `avatar_color`.
- Chats page: each chat shows its agent and an Allowed switch, with a Select to change the agent.
- Settings: the persona field moves to Palui on the Agents page.

### 19.8 Not in this round

Per-person roles (utusan IAM), group messages kept as background context, agents talking to each other, per-chat caps, media from guests.
