# RFC: Proactive

Status: accepted and built, 2026-11-24. Decisions are in section 17. Author: Palui agent session. Reviewer: the maintainer.
Scope: the design and its reasoning. Once accepted, the behavior moves to `docs/PLAN.md` (a new section next to 6c and 7, plus the tables in 9), and that section wins where the two differ.

## TL;DR

- Proactive lets an agent tell the owner things nobody asked for: "besok jam 9 kamu ke klinik", "30 menit lagi berangkat", "Pak Budi nanya dari jam 09:12, belum kamu bales", "pagi ini ada 3 hal".
- It's per agent and off by default. The owner turns it on for Palui only, at first.
- Every 30 minutes a tick collects from fixed sources without the model: the calendar, the owner's personal WhatsApp (wa-hub's "unanswered" list) and reminders. Each finding becomes a candidate with a stable key (`cal:<eventId>:depart`, `wa:<chatId>:<awaitingSince>`).
- A ledger (`nudges`) remembers what was already said, snoozed, done or muted, so nothing is told twice. That's the piece the Heartbeat preset lacks.
- Time-bound nudges ("berangkat", "meeting mulai") are scheduled to the minute with `wake_at` and rendered from a template, with no model call. Everything else goes through one `fast` model call per tick, with zero tools, only when there is something new.
- Delivery has brakes: quiet hours 22:00 to 07:00 (urgent calendar items pass), at most 4 messages per hour, one escalation per WhatsApp chat, a morning summary at 07:00 and an evening one at 20:00.
- Settings live on the agent page in a new Proactive tab, and in chat ("jam tenang jadi 23:00", "jangan ingetin grup kantor").

## 1. Goals and non-goals

Goals

1. The owner hears about what matters in their own day before they think to ask: where to be, when to leave, who's waiting for a reply.
2. Never the same thing twice. Every nudge has a key and a status, and the owner can close, snooze or mute it in one tap.
3. Cheap when nothing happens. A tick with nothing new costs zero tokens.
4. Predictable. Collection, keys, timing and limits are code, not prompt. The model only judges and words what the code found.
5. Untrusted content (other people's messages, invites from others) can change the wording of a nudge to the owner, never what the agent does.
6. Reuse: the scheduler timer, the outbox, owner actions, the gcal connector, MCP connectors, recall, budgets, taint.

Non-goals

- A news or threat radar (feeds, HN, CVEs scored for relevance). That's a different product. Section 15 says why.
- Acting for the owner. Proactive only tells. It never replies to a WhatsApp chat, accepts an invite or sends anything to anyone but the owner.
- Travel time from a maps API. v1 uses a fixed lead time.
- Proactive in shared chats. The target is always a chat whose audience is `owner`.

## 2. Where we start from

| What exists | What it gives | Why it's not enough |
|---|---|---|
| Heartbeat preset (`jobs/presets.go`) | A `fast` turn every 30 minutes that answers `NOTHING` to stay silent | No memory of what it said ("jangan mengulang" is only a prompt line), the model calls tools on every tick, "important" is a guess, no snooze or done |
| Daily briefing preset | A morning turn with tools | Same: a prompt, not a ledger. Becomes the morning summary here |
| Reminders | Exact-time messages with `rem:done` and `rem:snooze` | Only what the owner set up. Proactive reads them for summaries but doesn't re-send them |
| gcal connector | `ListEvents` (`connectors/gcal/calendar.go`) with id, title, start, end, location, status | No conference link field yet (section 5.1) |
| wa-hub over MCP | `list_chats tab=unanswered`: DMs waiting for a reply, oldest first | Text output, no JSON yet (section 5.2, decision 1) |
| Flows `poll` + `each` | A read tool on a timer, items deduped by key across runs | No per-item status, snooze, escalation, auto-close or summaries. Section 15 |
| Scheduler extensions (`reminders/scheduler.go`, `Extension`) | `Sweep` and `NextDue` on the shared timer | Proactive is one more extension |

## 3. Concepts

- Tick: one collection pass for one agent, every `checkEveryMin` minutes (default 30).
- Source: an adapter that turns one connector's data into candidates. v1 has three: calendar, WhatsApp, reminders.
- Candidate: something the owner might want to hear, with a stable key and a kind. Built fresh on every tick.
- Nudge: a candidate the ledger has seen, with a status. One row per key.
- Timed nudge: a nudge with a fixed moment (`wake_at`), sent by the scheduler at that minute from a template.
- Now nudge: a nudge with no fixed moment, decided and worded by the model in the tick that found it.
- Summary: the morning and evening messages, always sent at their time.

## 4. Data model

One numbered migration (`palui-schema-change`), three tables.

### 4.1 `agent_proactive`

| Column | Type | Notes |
|---|---|---|
| `agent_id` | text PK | References `agents.id` |
| `enabled` | integer | Default 0. Off for every agent until the owner turns it on |
| `chat_id` | text | Target chat. Must have audience `owner` (section 11) |
| `config` | text | JSON, section 4.4 |
| `next_tick_at` | integer | Unix ms, null when off |
| `last_tick_at` | integer | Unix ms. Null until the first tick, which only seeds chats already waiting |
| `tick_lease_until` | integer | Set while a tick runs; a lease past due means the tick died |
| `paused_reason` | text | Set when the engine pauses itself (target no longer `owner`, budget) |
| `created_at`, `updated_at` | integer | |

### 4.2 `nudges`

| Column | Type | Notes |
|---|---|---|
| `id` | text PK | `ndg_...` |
| `agent_id` | text | |
| `key` | text | Unique per agent: `cal:<eventId>` (tracking), `cal:<eventId>:depart`, `cal:<eventId>:start`, `cal:<eventId>:changed:<start>:<hash>`, `wa:<chatId>:<awaitingSince>`, `sum:morning:<date>`, `sum:evening:<date>` |
| `kind` | text | `cal_event` (an internal row per event that keeps its last snapshot, never sent), `cal_depart`, `cal_start`, `cal_changed`, `wa_waiting`, `sum_morning`, `sum_evening` |
| `subject` | text | What a mute matches: `wa:chat:<id>`, `cal:event:<id>` |
| `status` | text | `pending` (found, waiting to be told), `scheduled` (timed, waiting for `wake_at`), `firing` (claimed by a sweep), `sent`, `done`, `snoozed`, `dropped`, `expired`, `tracking` (`cal_event` rows) |
| `wake_at` | integer | For `scheduled` and `snoozed` |
| `sent_at` | integer | |
| `escalated_at` | integer | WhatsApp only, set once |
| `snapshot` | text | JSON of what the source showed (time, location, sender, a 200-character preview), used to detect changes and to render |
| `reason` | text | Why it was dropped or closed (`model: sticker`, `muted`, `replied`, `event cancelled`) |
| `outbox_id` | text | |
| `created_at`, `updated_at` | integer | |

Indexes: unique `(agent_id, key)`, `(status, wake_at)`.

### 4.3 `proactive_mutes` and `proactive_ticks`

`proactive_mutes`: `(agent_id, subject, created_at)`, unique on the first two. A mute matches every nudge with that subject, now and later.

`proactive_ticks`: one row per tick for the activity list and `explain`: `agent_id`, `started_at`, `finished_at`, per-source result (`ok`, `error`, count), candidates found, nudges sent, tokens, error. Kept 30 days.

### 4.4 Config

Versioned JSON, validated like settings (Go schema plus the zod copy for the dashboard).

```json
{
  "schema": 1,
  "checkEveryMin": 30,
  "maxPerHour": 4,
  "quietHours": { "start": "22:00", "end": "07:00", "urgentPasses": true },
  "morning": { "on": true, "at": "07:00" },
  "evening": { "on": true, "at": "20:00" },
  "calendar": { "connectorId": "gcal-1", "departLeadMin": 30, "onlineLeadMin": 10, "notifyChanges": true },
  "whatsapp": { "connectorId": "mcp-wa-hub", "account": "personal", "waitHours": 2, "escalateHours": 4, "maxAgeDays": 3, "skipGroups": true },
  "contactsAuto": true,
  "contacts": [{ "subject": "wa:chat:280", "label": "Pak Budi" }],
  "modelRole": "fast"
}
```

Bounds: `checkEveryMin` 15 to 120, `maxPerHour` 1 to 10, lead times 0 to 180, `waitHours` 1 to 24, `escalateHours` 1 to 24 or 0 for never, `maxAgeDays` 1 to 14. A source block that's missing or null means the source is off.

## 5. Sources (v1)

A source is Go code with one method: `Collect(ctx, cfg, now) ([]Candidate, error)`. Each runs with a 20 second timeout. A failed source is logged on the tick and the others go on. Three failed ticks in a row for one source send one owner notice per day ("WA personal nggak kebaca dari jam 10, cek connector wa-hub").

### 5.1 Calendar

- Reads `ListEvents` directly (not the `get_calendar` tool) from now to 36 hours ahead, on the connector named in the config.
- Small change to gcal: add the conference link (`hangoutLink` or `conferenceData` entry point) to `Event`.
- Per event:
  - Has a physical location (non-empty and not a URL): a `cal_depart` timed nudge at `start - departLeadMin`.
  - Has a conference link or a URL as location: a `cal_start` timed nudge at `start - onlineLeadMin`.
  - All-day events: summaries only.
  - Declined or cancelled events: dropped, and their scheduled nudges become `dropped` with reason `event cancelled`.
- Change detection: the snapshot keeps start, end, location and status. If they differ from the last snapshot and the event starts within 36 hours, a `cal_changed` now nudge is added and the timed nudges are moved.
- First tick after turning on: events are recorded without `cal_changed` nudges (a seed, like a flow `poll`).

### 5.2 WhatsApp (wa-hub)

- Calls `list_chats account=<account> tab=unanswered since=<now - maxAgeDays>` through the connector's server-side caller (the tool must be classified `read`). `since` keeps months-old waits out of the page.
- Today the output is text only. wa-hub adds a JSON form (MCP `structuredContent`) before phase 2, and the adapter reads only that (decision 1). A response without it fails the source with "wa-hub terlalu lama, butuh versi yang ada JSON-nya".
- Rules, in code, before any model:
  - Skip groups when `skipGroups`, chats muted in `proactive_mutes`, waits shorter than `waitHours` and waits older than `maxAgeDays`.
  - The key is the moment the wait began (`awaitingSince`): one wait is one nudge however many messages it holds, and a new wait after a reply is a new nudge.
  - A chat that has left the unanswered list closes its open nudge as `done` with reason `replied`.
- Important contacts: `contacts` is the manual list, always told (the model may word it but can't drop it). With `contactsAuto`, the decide call also gets the agent's memory profile (read once a day) to judge who else matters.
- First tick after turning on: chats already waiting are not nudged one by one. They show up as a count in the next summary.

### 5.3 Reminders

Read from the database for summaries only. Reminders already send themselves at their time with their own buttons, so a reminder nudge would be a duplicate.

## 6. The tick

```
next_tick_at reached
  │
  1. claim: UPDATE agent_proactive SET next_tick_at = now + every RETURNING ...
  2. collect: each source, 20 s timeout, failures logged
  3. reconcile with nudges:
       new key            -> insert
       known key          -> keep status; refresh snapshot
       snapshot changed   -> cal_changed candidate, move timed nudges
       gone from source   -> done (replied) or dropped (cancelled)
       subject muted      -> dropped (muted)
  4. timed candidates     -> status scheduled, wake_at set
  5. now candidates?      -> no: finish, 0 tokens
                          -> yes: decide (section 8)
  6. policy (section 9)   -> send now, hold, or merge into the next send
  7. one transaction: nudge rows to sent + one outbox row
  8. write proactive_ticks
```

The engine is a scheduler extension in `internal/proactive`. `NextDue` is the earliest of every enabled agent's `next_tick_at`, every `scheduled` or `snoozed` nudge's `wake_at`, and the next summary time. `Sweep` claims and runs them. `Recover` resets a tick claimed but never finished.

Turning proactive on, or the owner's "cek sekarang", sets `next_tick_at = now`.

## 7. Timed nudges

- At `wake_at`, the scheduler claims the nudge and renders it from a template. No model call, so a dead model provider never makes the owner late.
- Templates (bot-facing, casual Indonesian), with sanitized plain-text fields capped at 200 characters each:
  - `cal_depart`: `{name}, 30 menit lagi berangkat ke {title}, {location}.`
  - `cal_start`: `{title} mulai 10 menit lagi: {link}`
- Timed nudges that fall within 5 minutes of each other are sent as one message.
- A timed nudge whose event was cancelled or moved never fires: reconcile has already moved or dropped it.
- A timed nudge past its event's start by the time it's claimed (the server was down) becomes `expired`, not sent late.

## 8. Deciding and wording

One call per tick, only when there are now candidates, on the agent's `modelRole` (default `fast`).

- Zero tools. The turn is tainted. Its output is never saved to memory.
- Input:
  - the agent's persona, so the message sounds like the agent;
  - the owner's local time and today's already-sent nudges (titles only), so it doesn't repeat itself in other words;
  - a short recall profile (cached once a day from the agent's container);
  - the important-contact list;
  - the candidates, with every field from a source inside `<untrusted>` blocks.
- Output, with `ResponseFormat` JSON schema (`models/structured.go`), the same shape as `reminders/parse.go`:

```json
{
  "send": [{ "key": "wa:280:91822", "urgent": false }],
  "drop": [{ "key": "wa:46:1201", "why": "cuma stiker" }],
  "text": "Pak Budi nanya soal unit Banjarbaru dari jam 09:12, belum kamu bales."
}
```

- Keys not in the input are ignored. A candidate the model doesn't mention is held for the next tick, up to two ticks, then sent.
- `text` is sanitized like a tainted flow message: control characters out, plain text, capped at 700 characters.
- If the call fails or times out, important-contact candidates and `cal_changed` go out with a template, and the rest wait for the next tick.

## 9. Delivery policy

- Quiet hours: nothing is sent between `quietHours.start` and `end`. With `urgentPasses`, timed calendar nudges and a `cal_changed` for an event inside the next 3 hours still go out. WhatsApp nudges are never urgent. Everything held goes into the morning summary.
- Rate: at most `maxPerHour` proactive messages per agent in a rolling hour, summaries excluded. Anything over waits and is merged into the next send.
- Escalation: a `wa_waiting` nudge still open `escalateHours` after `sent_at`, not snoozed, gets one more message and `escalated_at`. Never a third.
- Summaries: at `morning.at` and `evening.at`, always sent, even when empty ("Besok kosong, nggak ada jadwal."). Morning: today's events, reminders due today, WhatsApp chats still waiting (count plus the top three by importance), what quiet hours held. Evening: tomorrow's events with times and locations, and the first time to leave. Summaries use the same decide call with a summary instruction, and they fall back to a plain template list when the model fails.
- Budget: decide calls count against the agent's `daily_token_cap` and the global budget. When the budget pauses turns, ticks still collect and timed nudges still fire (no tokens), but now nudges wait.

## 10. Owner actions

Every proactive message carries actions, shown the way reminder actions are on each channel:

| Action id | Effect |
|---|---|
| `ndg:done:<id>` | `done` |
| `ndg:snooze:<id>:<1h\|3h\|tomorrow>` | `snoozed`, `wake_at` moved. When it wakes, it's re-checked against the source first (a chat answered meanwhile closes silently) |
| `ndg:mute:<id>` | Adds the nudge's subject to `proactive_mutes`, nudge `dropped` |

A merged message carries actions for its first nudge only, plus "Selesai semua". Answering the WhatsApp chat on the phone also works: the next tick sees it left the unanswered list.

Chat tools, in a new `proactive` connector, offered only in owner turns and only for the turn's own agent:

- `get_proactive`, `update_proactive` (a merge patch on the config, plus `enabled` and `chat_id`)
- `mute_proactive`, `unmute_proactive` (by subject, or by name: "grup kantor" resolves through the last snapshots)
- `list_nudges` (today, or by status), `check_proactive_now`

## 11. Per agent and audience

- One `agent_proactive` row per agent, created on first save. Every agent starts off.
- A tick uses that agent's persona, recall container, model roles and token cap.
- Sources must be connectors this agent may use (`connectors.agent_ids`, migration 0023). Saving a config with a connector the agent can't use is refused.
- The target chat must have audience `owner` (the owner's DM or the web chat), checked at save and again at every tick. If it stops being `owner` (a participant was added), the agent pauses itself with `paused_reason` and tells the owner once. This keeps PLAN 19.4: a message never carries data that someone who can read the chat may not see. So the owner's personal WhatsApp can never feed a nudge into a shared chat.

## 12. Settings page

A new Proactive tab on the agent page (`apps/app/src/routes/_app/agents_.$slug.tsx`, after Tools). The API is `getProactive`, `updateProactive`, `listNudges`, `unmuteProactive`, `checkProactiveNow` under `/api/rpc`.

```
  Settings   Chats   Instructions   Memory   Tools   [Proactive]
 ─────────────────────────────────────────────────────────────────
 Status
   Proactive                                       [●━━] On
   Kirim ke        [ WhatsApp · owner DM               ▾ ]
   Cek terakhir    14:30 · berikutnya 15:00     [Cek sekarang]

 Ritme
   Cek tiap            [ 30 ] menit
   Ringkasan pagi      [ 07:00 ]  [●━━]
   Ringkasan malam     [ 20:00 ]  [●━━]
   Jam tenang          [ 22:00 ] sampai [ 07:00 ]
                       ☑ tetap kirim yang urgent
   Maks pesan          [ 4 ] per jam

 Sumber (cuma connector yang boleh dipakai agent ini)
   Kalender · gcal        ✓   berangkat -[30]m, online -[10]m, ☑ kabarin kalau berubah
   WhatsApp · wa-hub      ✓   belum dibales >[2]j, sekali lagi setelah [4]j, ☑ lewati grup
   Reminders                  hanya di ringkasan
   [+ Tambah sumber ▾]  Email, WA bisnis: belum didukung

 Kontak penting
   ◉ Otomatis dari memori + kebiasaan balas   ○ Manual aja
   <name> · auto   <name> · manual ✕   +8 lagi        [+ Tambah]

 Jangan diingetin
   Grup alumni        WA        sejak 20 Nov   [Buka lagi]
   Standup harian     Kalender  sejak 18 Nov   [Buka lagi]

 Aktivitas hari ini                          12 cek · 5 dikirim
   15:30  WA <name>       eskalasi terakhir     menunggu
   14:30  Berangkat ke klinik                   selesai
   11:30  WA <name>       belum dibales         ditunda 1j
   07:00  Ringkasan pagi                        terkirim
                                           [Lihat semua]

 Biaya
   Hari ini   8.420 token   dari cap agent 100k   · model fast

                                        [Batal]  [Simpan]
```

An agent with proactive off shows only the Status section, with the target picker limited to owner chats. The page states plainly that source contents (message previews, event titles) are sent to the model provider.

Fixtures: `worst` gets a long contact name, a 200-character preview, an event with a long location, a muted list of 50 and a day with 40 ticks.

## 13. Security and privacy

- Untrusted input: WhatsApp previews and names, and calendar titles, locations and descriptions from invites. The decide call has zero tools, so the worst a poisoned message can do is change the wording of one message to the owner. Timed nudges use templates with sanitized fields.
- Proactive never writes anywhere except the owner's target chat. It has no tools to call.
- Data kept: snapshots hold ids, names, times, locations and a 200-character preview. Nudges and ticks are pruned after 30 days.
- The wa-hub token lets Palui read every personal chat. The connector stays read-only (wa-hub has no write tools), and the token should stay scoped to one account.
- Turning proactive off stops ticks and drops `scheduled` nudges. The ledger stays, so turning it back on doesn't re-send old things.

## 14. Cost

- A tick with no now candidates: 0 tokens, three source calls (about 1 second of MCP on wa-hub).
- A decide call: roughly 1.5k tokens in and 200 out on `fast`.
- A typical day at 30-minute ticks: 48 ticks, about 8 to 12 decide calls and 2 summaries, around 25k tokens. Well under the Heartbeat preset's 100k cap, which does a tool-calling turn on every tick.
- Turning on proactive for Palui suggests pausing a Heartbeat job in the same chat, so the owner isn't told twice.

## 15. Rejected alternatives

- A relevance radar over feeds (RSS, HN, CVEs) with relevance, materiality and economy scoring, as in the "Proactive Agent" article that started this. Useful for a security team, but the owner's need is their own day: calendar, chats, commitments. Feeds can be a later source if wanted.
- A better Heartbeat prompt. No ledger, the model calls tools every tick, and "don't repeat yourself" can't be enforced by a prompt.
- Building it as a flow (`poll` + `each` + `prompt`). `each` dedups by key, but a flow has no per-item status, snooze, mute, auto-close on reply, escalation or summaries, and the settings would be a JSON definition instead of a page.
- Checking every 5 minutes. The owner chose 30. Timed nudges keep calendar reminders on the minute, and a 2-hour wait rule doesn't need finer polling.
- One global setting. The owner wants it per agent, with only Palui on.

## 16. Rollout

1. Schema, engine, calendar source, timed nudges, summaries, and `update_proactive` so it can be turned on from chat. Palui only in practice.
2. The wa-hub JSON PR first, then the WhatsApp source, important contacts, escalation, owner actions.
3. The Proactive tab, the remaining chat tools, fixtures, the PLAN section.

Each phase ships behind `enabled` and is safe to merge alone.

## 17. Decisions

1. wa-hub output: wa-hub gets a JSON form of `list_chats` (MCP `structuredContent`) in a small PR to `zakiego/wa-hub`, landing before phase 2. The adapter reads only that, so a change to the text wording can't break it. Text parsing was rejected as fragile.
2. Travel time: a fixed lead time in v1. A maps API from home or current location is a later change to `cal_depart` only.
3. More sources: not in v1. Email (gws) and the business WhatsApp account are one adapter each, later.
4. Snooze: 1h, 3h and tomorrow, the same choices as reminders.
5. Heartbeat preset: kept for agents without proactive. Turning proactive on suggests pausing a Heartbeat job in the same chat (section 14).
6. Learning from mutes and "done without replying": not in v1. Important contacts stay deterministic (recall, reply stats, manual), so behavior is easy to predict and test. Revisit once `nudges` has a few weeks of data.
