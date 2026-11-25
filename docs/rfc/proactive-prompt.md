# RFC: Proactive from a prompt

Status: accepted and built, 2026-11-25. Decisions are in section 16. Author: Palui agent session. Reviewer: the maintainer.
Replaces the sources, rules and templates of `docs/rfc/proactive.md` (accepted 2026-11-24). Its ledger, delivery policy and owner actions stay. Once accepted, PLAN 6d is rewritten from this RFC.

## TL;DR

- Today proactive is two hard-coded pipelines: a calendar source and a WhatsApp source, each with fixed knobs (`departLeadMin`, `waitHours`, `skipGroups`), fixed nudge kinds and Go templates. Adding email or GitHub means Go code. The WhatsApp source only works with wa-hub, and the MCP paths match MCP Hub's tool names by regex. Palui is open source, so neither is acceptable.
- New shape: one proactive prompt per agent, written by the owner, that mentions the tools it may read with `@<tool>`. Everything that should be predictable is a setting: when it runs, where it sends, quiet hours, rate, summaries, model, budget.
- The prompt is the flexible part ("tell me 30 min before I have to leave", "only clients and family, skip banks"). The settings and the engine are the fixed part: nothing is said twice, timed messages go out on the minute, quiet hours and the hourly cap hold whatever the prompt says.
- A mention is the whole tool scope of the run. The editor autocompletes it from the agent's read tools. A mention that doesn't resolve is refused at save and fails the run loudly.
- Proactive has its own memory: short notes carried between runs, written by the model ("Pak Budi bilang kirim dokumen besok") and by the owner ("yang dari bank nggak usah"), visible and editable on the tab.
- Sources stop being proactive code. Any read tool on any connector is a source. Calendar and WhatsApp come back as standard-protocol connectors (Google Calendar, an ICS feed, WhatsApp inbox mode on whatsmeow), each a separate PR.

## 1. Goals and non-goals

Goals

1. The owner decides what matters in plain words, and can add a source without a Palui release.
2. Only standard protocols. A stranger self-hosting Palui can reproduce every example in the docs with public software.
3. Keep the guarantees of v1: never the same thing twice, timed messages on the minute, quiet hours, a rate cap, one-tap done, snooze and mute.
4. Fail loudly. A broken mention or a dead connector is a visible error on the tab and one owner notice a day, never a silent empty run.
5. Untrusted content can change the wording of a message to the owner, never what the agent does.

Non-goals

- Several proactive prompts per agent. One combined prompt (owner decision). Two concerns are two paragraphs.
- Acting for the owner. Proactive still only tells. Only read tools can be mentioned.
- Replacing flows. Flows stay for deterministic multi-step work with writes and approvals.
- Sending anywhere but the owner. The target is still a chat whose audience is `owner`.

## 2. What's flexible and what's fixed

| Flexible (the prompt) | Fixed (settings and engine) |
|---|---|
| Which tools to read and with what arguments | Which tools are allowed: exactly the mentioned ones, read class only |
| What counts as important, who matters, what to skip | Check every N minutes |
| Lead times ("30 min before", "10 min before online calls") | Quiet hours and whether urgent items pass |
| When to remind again | Max messages per hour |
| Wording | Output language (the global `language` setting) |
| | Morning and evening summary times, on or off |
| | Target chat (owner audience only) |
| | Model role and the agent's daily token cap |
| | Output: plain text, at most 1,500 characters per message. The prompt sets the wording of what goes out now (`message`); no buttons, replies act on it (done, snooze 1h, 3h, tomorrow, mute) |
| | Dedup by key, timed sends, expiry of stale items |

## 3. Mentions

Syntax: `@<connectorId>__<tool>`, the same namespaced name the model sees (`@gcal__get_calendar`, `@github__list_notifications`). It's plain text in the stored prompt, so it diffs and exports cleanly.

Editor (agent page, Proactive tab):

- Typing `@` opens a combobox over the tools this agent may use, grouped by connector, filtered to class `read`. Each row shows the connector label, the tool title and an "untrusted" badge where the tool has one.
- A resolved mention renders as a chip with the connector label. An unresolved one renders as a red chip with the reason (connector removed, off, not allowed for this agent, tool gone, not read).
- Write and destructive tools are listed greyed out with "proactive can only read", so the owner learns why instead of not finding it.

Validation:

- At save: at least one mention; every mention resolves to a tool on a connector this agent may use (`connectors.agent_ids`), classified `read`; at most 10 mentions; prompt at most 4,000 UTF-16 units. A failure refuses the save and names the mention.
- At every run: the same check. Any mention that fails to resolve fails the whole run with that mention named, even when the others resolve (decision 1). Three failed runs in a row set `paused_reason` and tell the owner once. One failure notice a day at most.
- Scope: the run's tool set is exactly the mentioned tools. `palui__search_tools` and every other tool are absent, so the model can't wander.

## 4. The run

```
next_tick_at reached
  │
  1. claim (lease, as today)
  2. resolve mentions            -> any missing: fail the run, log it, stop
  3. turn on the agent's modelRole
       tools: the mentioned tools only
       input: persona, owner's local time, the prompt,
              open items (key, text, status, sent_at, wake_at) from the last 3 days,
              mutes (subjects), proactive memory (section 8)
       output: JSON (section 5), ResponseFormat schema
  4. reconcile items with the nudges ledger (section 6)
  5. policy: quiet hours, rate, merge (unchanged from v1 section 9)
  6. one transaction: nudge rows + outbox row
  7. proactive_ticks row: mentions called, tokens, items, error
```

The turn is tainted from the first untrusted tool result. Its output is never saved to memory. Tool calls go through the normal server-side caller with `CallScope{AgentID}`, so classes and per-agent allowlists are enforced there too.

Cost guard: without a brake, a run with nothing new costs one turn, unlike v1's zero-token ticks. Three brakes, all in the first version (decision 2):

1. No runs inside quiet hours (section 9), and the agent's daily token cap bounds the rest. The tab shows today's tokens next to the cap.
2. Replay skip: before the turn, Palui replays the exact read calls the model made on the last full run (same tools, same arguments) and hashes the results. If every hash matches, the memory and the open items are unchanged, and no owner reply arrived, the turn is skipped (0 tokens, logged as `skipped: unchanged`). Any replay error, or a run with no recorded calls, means a full turn.
3. A full turn still runs at least every 2 hours, and always for summaries, so things entering a time window ("the next 36 hours" moves) are seen even when replayed results look the same.

Timed items need no turn at all: they fire from the ledger.

## 5. Output contract

```json
{
  "items": [
    {
      "key": "cal:3k9a:leave",
      "subject": "cal:event:3k9a",
      "text": "30 menit lagi berangkat ke klinik, Jl. A. Yani km 4.",
      "sendAt": "2026-11-25T08:30:00+08:00",
      "urgent": true
    },
    {
      "key": "wa:280:1764052320",
      "subject": "wa:chat:280",
      "text": "Pak Budi nanya soal unit Banjarbaru dari jam 09:12, belum kamu bales."
    }
  ],
  "closed": [{ "key": "wa:46:1764040000", "why": "udah dibales" }],
  "remember": [{ "text": "Pak Budi janji kirim dokumen unit Banjarbaru besok pagi.", "days": 3 }],
  "forget": ["pmem_8f2k"]
}
```

- `key`: identifies one thing to say. The system prompt tells the model: same thing, same key; when a change deserves its own message, put the change in the key (`cal:3k9a:moved:0930`). At most 200 characters, `[A-Za-z0-9:._-]`.
- `subject`: what a mute matches. Defaults to the key up to its second colon.
- `text`: plain text, sanitized like a tainted flow message, at most 300 characters per item.
- `sendAt`: optional. Makes the item timed (section 6). Must be in the future and within 48 hours, else the item is sent now if `sendAt` is at most 10 minutes past, and dropped otherwise.
- `urgent`: may pass quiet hours, only when the setting allows it. At most 3 urgent items a day per agent, the rest are treated as normal (a prompt can't turn quiet hours off).
- `closed`: keys the model says are resolved (chat answered, event cancelled). Their open or scheduled nudges become `done` or `dropped` with `why`.
- `remember` and `forget`: memory notes to add (with an optional lifetime in days) or remove by id (section 8).

If the turn fails or returns invalid JSON, nothing new is sent and scheduled items still fire with their stored text.

## 6. Ledger and timing

The `nudges` table stays. Kinds become `item`, `sum_morning`, `sum_evening`. No new migration: the kinds are text and the config is JSON.

| Item from the model | Ledger state | Result |
|---|---|---|
| new key, no `sendAt` | none | `pending`, goes through policy now |
| new key, `sendAt` | none | `scheduled` at `sendAt` with its text |
| known key | `scheduled` | text and `wake_at` updated (a moved event moves its reminder) |
| known key | `sent`, `done`, `dropped`, `expired` | ignored: never twice |
| known key | `snoozed` | ignored until it wakes |
| subject muted | any | `dropped` (muted) |
| key in `closed` | `pending`, `scheduled`, `snoozed` | `done` or `dropped` |

A timed item fires at its minute with its stored text, no model call. One that is claimed more than 10 minutes late (server down) becomes `expired`. Timed items within 5 minutes of each other go out as one message.

Escalation is no longer a rule. The model sees open items with `sent_at`, so "kalau 4 jam belum dibales, ingetin sekali lagi" is a new key it may emit (`wa:280:1764052320:again`).

## 7. Summaries

The morning and evening times and switches stay settings. At summary time the same turn runs with a summary instruction appended: what quiet hours held, open items, and what the prompt says the summary should cover. The owner can steer it in the prompt ("ringkasan pagi: jadwal hari ini dan siapa yang nunggu"). If the turn fails, a template lists held and open item texts.

## 8. Memory

A run only sees what the tools return right now and the open items. Some things worth knowing don't live in any tool: "Pak Budi said he'd send it tomorrow", "the owner doesn't care about the bank group", "this client always answers late, don't escalate". Proactive keeps short notes for that.

Notes (`proactive_notes`, one new migration):

| Column | Notes |
|---|---|
| `id` | `pmem_...` |
| `agent_id` | |
| `text` | At most 300 UTF-16 units, sanitized plain text |
| `source` | `owner` (typed on the tab, said in chat, or a reply to a proactive message) or `model` (from `remember`) |
| `expires_at` | `model` notes: `days` from the output, 1 to 30, default 7. `owner` notes: null, they never expire |
| `created_at`, `updated_at` | |

Where notes come from:

1. The model, through `remember` and `forget` in its output. It's told to keep commitments, patterns and things the owner said, not tool data it can read again.
2. The owner on the tab: add, edit, delete, and "keep" (turns a `model` note into an `owner` note with no expiry).
3. The owner replying to a proactive message with something that isn't an action ("done", "tunda 3 jam", "jangan ingetin" stay actions). The reply text becomes an `owner` note tied to that message's items, and the next run is pulled forward to now so the owner sees it was heard.
4. Chat tools: `remember_proactive`, `forget_proactive`, `list_proactive_notes` (owner turns only).

Limits: 50 notes per agent and 4,000 units in total in the prompt. Over the limit, the oldest `model` notes are dropped first. `owner` notes are never dropped automatically; a new one over the limit is refused with "memori proactive penuh, hapus yang lama dulu".

In the run, notes go in their own block, `owner` notes first, labeled by source. The system prompt says owner notes are instructions from the owner and model notes are context, not instructions.

The agent's long-term memory (recall) is separate and stays separate: proactive doesn't write to it. A run can still read it by mentioning the memory connector's read tool like any other tool (`@recall__search`).

Mutes stay their own table: a mute is exact and enforced by the engine, a note is guidance for the model.

## 9. Config

```json
{
  "schema": 2,
  "prompt": "Check @gcal__get_calendar for the next 36 hours. ...",
  "checkEveryMin": 30,
  "quietHours": { "start": "22:00", "end": "07:00", "urgentPasses": true },
  "maxPerHour": 4,
  "morning": { "on": true, "at": "07:00" },
  "evening": { "on": true, "at": "20:00" },
  "modelRole": "fast"
}
```

`quietHours` is one setting for two things (decision 4): no runs happen inside it, and nothing is sent inside it. Timed items scheduled before quiet hours still fire inside them only when `urgentPasses` and the item is urgent; the rest wait for the morning summary. Skipping runs at night is most of the token saving. A separate "active hours" was rejected as one more thing to get wrong for an edge case (catching an event moved at 05:00).

## 9a. Language

- The system prompt, the starter prompt, the docs examples and every field label are English (decision 5). The owner may still write the prompt in any language.
- What gets sent (items, summaries, the fallback templates) follows the global `language` setting: `id` or `en`, and `auto` means the language of the owner's recent messages in the target chat, falling back to casual Indonesian (PLAN D6). The run's system prompt states the output language explicitly, so an English prompt still produces Indonesian messages for an Indonesian owner.
- Memory notes keep the language they were written in.

Upgrade from schema 1 (`upgradeConfig`, no data migration): the timing, quiet hours, rate, summaries and model carry over. `calendar`, `whatsapp`, `contacts` and `contactsAuto` are dropped. An agent that had proactive on is paused with `paused_reason` "Proactive now runs from a prompt. Write one on the Proactive tab." and the editor opens with a starter prompt built from its old values (lead times, wait hours, contact names) with no mentions filled in.

## 10. Sources are connectors

Proactive has no source code anymore. Whatever a connector exposes as a read tool can be mentioned. To cover the v1 use cases with standard protocols, three connector changes, each its own PR and its own small RFC note:

| Need | Connector | Protocol | Tool |
|---|---|---|---|
| Google Calendar | `gcal` (exists) | Google Calendar API, owner's OAuth client | `get_calendar` (add the video link and the owner's response to its output) |
| Any calendar | `ics` (new) | iCalendar feed URL (RFC 5545), secret link from Google, Outlook, iCloud, Fastmail, Nextcloud | `get_events` from, to |
| Personal WhatsApp | `wa-inbox` (new type, on the whatsmeow runtime of the channel, read only) | WhatsApp multi-device, linked as a companion | `waiting_chats`, `list_chats` |

WhatsApp inbox is a separate connector type, not a flag on the channel connector, so no code path can make it answer in the owner's personal chats: it reuses the channel's runtime with `ReadOnly` (no owner identity written, nothing routed, no channel handle for the outbox) and an observer that sees the owner's own messages too. It keeps per chat only the id, name, kind, the time of the last message in, the owner's last reply, how many messages wait and a 200-character preview (`wa_inbox_chats`, one migration), never full bodies. It starts empty: there's no history sync.

Removed: the calendar MCP path (`CalendarSource.Tool`, `ParseMCPEvents`, `calendarToolRe`), the wa-hub path (`WhatsAppSource`, `ParseWaiting`, `whatsAppToolRe`, wa-hub triage from #28), `CalendarCandidates`, `WhatsAppCandidates`, the decide call, the v1 templates, and the docs that name wa-hub or MCP Hub.

## 11. Chat tools

The `proactive` connector keeps `get_proactive`, `update_proactive` (merge patch, `prompt` included and validated like the dashboard), `mute_proactive`, `unmute_proactive`, `list_nudges`, `check_proactive_now`, and adds the memory tools from section 8. An `update_proactive` that adds a mention the agent can't use is refused with the reason, same text as the editor.

## 12. Settings page

```
  Settings   Chats   Instructions   Memory   Tools   [Proactive]
 ─────────────────────────────────────────────────────────────────
 Status
   Proactive                                       [●━━] On
   Send to          [ WhatsApp · owner DM               ▾ ]
   Last run         14:30 · next 15:00            [Run now]

 Prompt
   ┌───────────────────────────────────────────────────────────┐
   │ Check [Google Calendar · get_calendar] for the next 36    │
   │ hours. If an event has a place, tell me 30 min before I   │
   │ have to leave. If it's online, 10 min before it starts.   │
   │                                                           │
   │ Check [WhatsApp inbox · waiting_chats]. Tell me about DMs │
   │ waiting over 2 hours, only clients and family. Skip       │
   │ groups and banks. @|                                      │
   │                 ┌──────────────────────────────────┐      │
   │                 │ Google Calendar  get_calendar    │      │
   │                 │ GitHub           list_notifs  ⚠  │      │
   │                 │ GitHub           create_issue  ✕ │      │
   │                 └──────────────────────────────────┘      │
   └───────────────────────────────────────────────────────────┘
   2 tools · read only                        [Test run]

 Schedule
   Check every          [ 30 ] min, outside quiet hours
   Morning summary      [ 07:00 ]  [●━━]
   Evening summary      [ 20:00 ]  [●━━]
   Quiet hours          [ 22:00 ] to [ 07:00 ]   ☑ urgent passes
   Max messages         [ 4 ] per hour
   Model                [ fast ▾ ]

 Memory                                   12 notes · 1,840 / 4,000
   Yang dari grup bank nggak usah             you     ✎ ✕
   Pak Budi janji kirim dokumen besok pagi    model · 2 days left   [Keep] ✕
   [+ Add note]

 Muted · Activity today · Cost      (as in v1)
```

Test run: a dry run that calls the mentioned tools for real and shows the items the model would send, without writing nudges or sending anything. Fixtures: `worst` gets a 4,000-unit prompt, ten mentions with long connector labels, an unresolved mention, and items with 300-character texts.

## 13. Security

- Mentions are an allowlist checked twice (save and run), and the caller enforces class and agent scope a third time.
- Only read tools, so a poisoned WhatsApp message or invite can at worst change the wording of one message to the owner, or ask for a timed item. `urgent` is capped so it can't flood quiet hours.
- Memory poisoning: a poisoned message could make the model `remember` something that sticks across runs. Model notes are labeled as context, expire within 30 days, are capped, sit below owner notes, and are listed on the tab. They still can't widen the tool scope, the target or the caps, which aren't in the prompt's reach.
- Output goes only to the owner-audience target, checked at every run.
- The page says plainly that the results of the mentioned tools are sent to the model provider.

## 14. Rejected alternatives

- Keep fixed sources and add knobs. Every new need is a release, and the knobs never match how people think about importance.
- Several watches per agent. More to manage. The owner chose one combined prompt.
- Build it on flows (`poll` + `each` + `prompt`). Flows are JSON built through chat, with no per-item snooze, mute or timed send, and the owner wants to write a prompt, not a graph.
- Palui calls the mentioned tools itself and the model gets zero tools (v1's safety shape). It needs an argument language in the prompt (`@gcal(from: now, to: +36h)`), which is the rigidity this RFC removes. Read-only scope gets most of the same safety.
- Let the prompt pick the target chat or the output format. Those are the fixed part on purpose (owner decision).
- Write proactive notes into recall. Recall is the agent's memory of the owner across every chat; short-lived run notes ("dokumen besok") would clutter it, and a tainted turn shouldn't write there.
- Keep state only in item keys and open items. It can't hold things that aren't an item yet, or what the owner said in a reply.

## 15. Rollout

1. This RFC, then PLAN 6d.
2. Engine: schema 2 config and upgrade, mention parsing and validation, the run turn, the output contract, ledger reconcile, replay skip, `proactive_notes` (migration) and the memory tools; delete the v1 sources. Proactive pauses on upgrade with the starter prompt.
3. Dashboard: prompt editor with mention autocomplete, schedule settings, memory list, test run.
4. Connectors, in any order: `gcal` output additions, `ics`, WhatsApp inbox.
5. Docs and changelog: the Proactive page rewritten around the prompt, no wa-hub or MCP Hub.

Steps 2 and 3 ship together. Each connector in step 4 is useful alone.

## 16. Decisions

1. Partial failure: one mention that doesn't resolve fails the whole run, even when the others resolve (owner, 2026-11-25).
2. Replay skip ships in the first version, with a full turn at least every 2 hours (section 4). Without it a quiet day costs about 30 turns for nothing.
3. Memory: proactive keeps its own notes (section 8), separate from recall (owner asked for memory, 2026-11-25).
4. One quiet hours setting: no runs and no sends inside it (section 9). No separate active hours.
5. Language: prompts and labels are always English; the output follows the global `language` setting (section 9a, owner, 2026-11-25).
6. Owner replies to a proactive message become notes by default (section 8, item 3). It's the owner's own chat, the note is listed on the tab and deletable, and it's the most natural way to correct the agent.
