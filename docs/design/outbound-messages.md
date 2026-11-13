# Design: outbound messages (Palui messages someone on the owner's behalf)

Status: reviewed and built, 2026-11-13. Author: Palui agent session. Reviewer: Zaki.

## TL;DR

- One tool, `outbound__send_message`: recipient (saved contact name, or a number plus a name), the exact text, and an optional time or RRULE. No time sends now, held 15 seconds so it can be cancelled.
- Contacts are a small table, scoped per agent like instructions (PLAN 19): each agent has its own list, and a turn or flow only sees its own agent's contacts. Each contact has a send policy: `ask` (default, approval every time) or `allow` (skip approval for messages the owner asked for himself). `allow` can only be set from the dashboard.
- Guards live in code: the tool only exists in owner turns started by an owner message, a new number always needs approval, approval shows the recipient, the exact text and the time, tainted turns always ask, and `allow` only skips approval when the owner's own message names the contact. Rate limits and a global kill switch are checked when the tool runs, when a scheduled send fires, and again in the outbox worker.
- Scheduled and repeating sends get their own small table swept by the existing scheduler (a `SchedulerExtension`, like flows). They reuse the reminders' time and RRULE code and the outbox. They don't reuse the `reminders` table.
- Conditional sends are flows: the `message` step gets a literal `contact` field, approved once per flow version like every other flow write.
- Every send is an outbox row with source `outbound:<id>`, so the existing outbox is the log. The dashboard gets one page, `/app/outbound`: kill switch and limits, contacts, scheduled sends, and the log, filterable by agent.

## 1. What exists today (verified in code)

- Reminders only go to the chat they were set in. `ownReminder` (`packages/connectors/src/reminders/runtime.ts`) rejects any other chat, and the scheduler sends to `reminders.chat_id`.
- A flow `message` step has `to` (`packages/core/src/flows/schema.ts`). It must be a literal chat id that already exists and is the owner's or allowed (`validate.ts`, `chatAccess`). A number that never wrote to Palui has no chat row, so it can't be a target. That matches what `search_chats` showed.
- No model tool sends to another chat. The outbox (`packages/core/src/outbox`) sends to any chat row it's given and checks nothing about the recipient: no allowlist, no rate limit.
- Approvals (`packages/core/src/chat/turn.ts` `toolApproval`): destructive always asks, write asks when the turn is tainted or "strict" (schedule, reminder, flow, hook, shared chat), otherwise runs. The prompt shows `preview(args, 300)`, so a long text is cut off in the prompt. On approval the SDK runs the stored call, so the args that run are exactly the args the model emitted. Nothing re-generates them.
- Taint: a turn is tainted by an untrusted tool call or result (web fetch, chat search, calendar, external MCP), by a hook, or by quoting someone else. Auto-recall does not taint, and guest chat text can land in memory.

### Which number WhatsApp sends from

The WhatsApp connector is a linked device of `pairPhone` (saved as `linkedNumber`). `ownerPhone` is the number Palui answers. The socket drops `fromMe` messages (`whatsapp/runtime.ts`, `deliver`), so the owner can't chat with Palui from the linked account itself. Palui therefore runs on its own number, and every outbound message reaches the recipient from Palui's number, not Zaki's.

What that means:

- The recipient sees a number they may not have saved. The text has to say who it's from ("Halo, ini asisten Zaki. Zaki lagi otw ya"). The tool tells the model so, and a configurable signature line can be appended.
- No impersonation of Zaki's own account is possible, which is good.
- A brand new number messaging strangers is the classic WhatsApp ban pattern. Low limits and the allowlist matter for that, not only for safety.
- A recipient's reply goes to Palui's number. Their chat is not allowed, so the router drops it. Forwarding contact replies to the owner is a follow-up (section 10).

## 2. Review of the draft

The draft holds up. Changes and additions:

1. **One tool, yes. Add contacts inline instead of a separate "add contact" step.** "chat Zaki (082200001001) sekarang" should be one approval, not two. `send_message` accepts a number plus a name. When the number isn't saved, the call always needs approval, and the prompt says "kontak baru". Approving saves the contact (policy `ask`) and sends. Contacts can also be added on the dashboard.
2. **"Owner-origin only" needs a precise rule.** A turn started by the owner can still read a web page mid-turn. The rule in code:
   - The tool is offered only when the turn's origin is an owner message (`trigger` message, or an approval resume of one) in an owner-mode chat. Schedule, reminder, flow, hook and guest/shared turns don't get it, and `execute` refuses again if called anyway.
   - A tainted turn always asks, whatever the contact's policy. The prompt shows the exact text, so injected content can't send without the owner reading it.
   - `allow` skips approval only when the turn is untainted and the owner's own messages in this batch mention the contact (name, alias or number). Text the owner never typed (recall, notes, history) can't pick an `allow` contact. This closes the auto-recall gap without touching recall.
3. **Exact text.** Approval shows the full text (a tool-specific preview, not the 300-character JSON cut). The approved call is replayed as stored, so what was shown is what goes out. The signature, if set, is shown in the preview too. For `allow` sends there is no preview, so the owner gets a notice with the exact text and a cancel button, and the 15-second hold applies.
4. **`allow` can't be set from chat.** Raising trust is a dashboard action (passkey login). That way no chat content, injected or not, can widen what runs without approval.
5. **Approval on a different channel.** Already handled: an answer only counts from the chat that was asked (`decideApproval`), or from the dashboard. Nothing new.
6. **Scheduled sends after a contact is removed.** Removing a contact cancels its scheduled sends and its held sends. A flow version whose `contact` points at a removed contact fails that step with a clear error.
7. **Quiet hours.** Owner-scheduled sends go out at the time the owner chose. Flow sends follow the flow's own triggers. No separate quiet-hours rule in v1 (open question).
8. **Telegram recipients.** A Telegram bot can only message people who started it. So Telegram contacts can only be made from an existing Telegram chat, on the dashboard. Numbers always mean WhatsApp.
9. **Number normalization.** `08xx` → `628xx`, `+62 8xx` → `628xx`, `628xx` stays, spaces, dashes, dots and brackets are stripped. Other country codes need the `+`. 10 to 15 digits. The recipient chat is `628xx@s.whatsapp.net`. LIDs only appear on inbound messages; replies are dropped anyway in v1.
10. **Delivery failures.** Reuse the outbox: retries with backoff, ack before `sent`. Outbound rows expire after 1 hour (a stale "otw" is worse than none), never fall back to another channel, and are cancelled, not rerouted to the owner, when their channel is removed. A failed send tells the owner once.
11. **Rate limits** count outbox rows with source `outbound:%` in the last 24 hours that weren't cancelled. Defaults: 5 per contact, 20 total. A send over the limit is refused with the numbers. A scheduled occurrence over the limit is skipped and the owner is told.

## 3. Alternatives considered

- **Reuse the `reminders` table for scheduled sends.** The time and RRULE code is the valuable part, and it's reused as functions. The table carries owner semantics everywhere: the list and snooze tools, Done/Tunda buttons, the "telat" note, channel fallback to the owner. Every one of those would need a branch, and a missed branch sends a contact's message to the wrong place. A separate table with a 60-line sweep is less code and fails safer.
- **Do everything with flows.** "tiap Jumat jam 11 kabarin X" could be a one-step flow. Flows need a definition, validation and a version approval, which is a lot for "send this at 8". Flows stay the tool for conditions.
- **A separate queue/worker for outbound.** The outbox already has claims, retries, acks, timeouts and pause handling. A parallel worker would duplicate all of it. The only additions are three small checks in the existing worker.
- **Per-message approval only, no `allow`.** Simplest and safest. Zaki decided on per-contact levels, and the guards above keep `allow` narrow.

## 4. Data model

```ts
// contacts: people an agent may message for the owner.
contacts {
  id            text pk           // "con_..."
  agentId       text              // the agent whose list it's on (null = default agent)
  name          text not null     // "Zaki", unique among the agent's active contacts (case-insensitive)
  channel       "whatsapp" | "telegram"
  address       text not null     // WhatsApp digits "6282200001001", or the Telegram chat's external id
  chatId        text not null     // the chat row outbox sends to (created with allowed=false for a new number)
  policy        "ask" | "allow"   // default "ask"
  note          text              // optional, shown to the model ("adik", "kantor")
  createdAt, updatedAt
  removedAt     integer           // soft delete, keeps the log readable
}

// outbound_messages: one row per request, a one-off or a series.
outbound_messages {
  id            text pk           // "obm_..."
  agentId       text              // copied from the contact
  contactId     text not null
  text          text not null     // the exact text, signature already applied
  origin        "chat" | "flow"
  originRef     text              // run id, or flow run id
  approvedBy    "owner" | "policy" | "flow_version"
  // when: null rrule + fireAt = one-off; rrule = series (wall clock in tz)
  rrule, tz, localStart
  fireAt        integer           // next send time, UTC ms
  status        "scheduled" | "firing" | "done" | "cancelled"
  lastOutboxId  text
  createdAt, updatedAt
}
```

Each actual send is an outbox row: `chatId` = the contact's chat, `source` = `outbound:<outboundId>`. "Now" sends get `notBefore = now + 15 s` and `expiresAt = now + 1 h`.

Settings, one new key:

```ts
outbound: {
  enabled: boolean            // the kill switch, default true
  perContactPerDay: number    // default 5
  perDay: number              // default 20
  holdSeconds: number         // undo window, default 15
  signature: string           // appended on its own line when set, default ""
}
```

### Per agent

- The turn's `agentId` picks the list. The owner talking to Palui sees Palui's contacts; the owner DMing an agent's own Telegram bot sees that agent's. A flow uses the flow's agent.
- The same number can be on two agents' lists. Both point at the same chat row; names and approval settings are separate.
- The per-contact limit counts per contact. The daily total and the kill switch stay global, because every agent shares one WhatsApp number and the ban risk is per number.
- Deleting an agent removes its contacts and cancels its scheduled and held sends.

## 5. Tools (connector `outbound`, built in)

| Tool | Class | What it does |
| --- | --- | --- |
| `send_message({ to, name?, text, localDateTime?, rrule?, tz? })` | write, custom approval | Send now (held), at a time, or on a repeat. `to` is a contact name or a number. A new number needs `name`. |
| `list_contacts()` | read | Contacts with their policy and note. |
| `add_contact({ name, phone, note? })` | write, always asks | Save a WhatsApp contact the owner named in chat ("tambah kontak Ana +62 822..."). Added after v0.8.0. |
| `list_outbound_messages({ filter })` | read | Scheduled series and recent sends with status. |
| `cancel_message({ id })` | write | Cancel a scheduled send or series, or a held "now" send. |

Approval rule for `send_message` (in addition to the turn's own rule):

```
ask unless all of:
  contact exists and policy == "allow"
  turn not tainted, origin == message, mode == owner
  the owner's batch text mentions the contact
```

`execute` refuses (no approval can override) when: outbound is off, origin isn't an owner message, mode isn't owner, the number is invalid, a name points at no contact, the rate limit is hit, or the time is in the past.

To make that possible, `PaluiTool` gets two optional fields, and `TurnInfo` gets the turn's origin facts:

```ts
PaluiTool.needsApproval?: (input: unknown) => boolean   // extra "ask" on top of class rules
PaluiTool.preview?: (input: unknown) => string          // approval prompt text instead of the JSON cut
TurnInfo.origin?: "message" | "schedule" | "hook" | "reminder" | "flow"
TurnInfo.mode?: "owner" | "guest" | "sharedOwner"
TurnInfo.tainted?: () => boolean
TurnInfo.ownerText?: string                             // the owner's messages in this batch
```

`add_contact` has no policy argument, so a new contact always starts at `ask`. "Always allow" stays dashboard-only, and there's no tool to change a contact's policy. Its rule:

```
ask whenever the call would save something (new number, free name)
no ask when nothing gets saved: the number is already a contact (returns it, "sudah ada"),
  the name belongs to another number, or the number is invalid (execute refuses with the reason)
```

`needsApproval` runs before the agent's tool permissions, so "allow all tools" or an `allow` entry for it can't skip the ask. `execute` saves only in the run the approval resumed (`trigger == "approval"`), and only in turns the owner started in his own chat. Flows, hooks, schedules and guests never see the tool, the same gate as `send_message`, so a flow step can't add contacts.

Router: a callback `ob:cancel:<id>` and a reply "batal" quoting the hold notice cancel a held send. A bare "batal" with no approval waiting cancels the newest held send in that chat.

## 6. Flows

`messageStepSchema` gets `contact?: string` (a contact id or name, literal, never a template). Exactly one of `to` and `contact` may be set.

- Validation resolves the contact. Unknown → problem. Known → a write call `{ kind: "message", to: "contact:<id>" }`, so the version needs the owner's approval like any other write. `allow` doesn't skip it: the flow decides when to send, not the owner.
- At run time the step checks the kill switch, that the contact still exists, the rate limit, and that the text isn't tainted (a contact message built from tool output or webhook data is refused). Then it creates an `outbound_messages` row (origin `flow`, approvedBy `flow_version`) and enqueues the outbox row without a hold.

## 7. Dashboard

One new page, `/app/outbound` ("Outbound" in the sidebar, next to Reminders). With more than one agent it has an agent filter, every row names its agent, and Add contact asks which agent it's for:

1. Header: the kill switch, and today's count against the limits. Limits and signature are edited in a small form.
2. Contacts: name, number or chat, policy toggle (`Always ask` / `Always allow`), remove. Add by name plus number, or from an existing Telegram chat.
3. Scheduled: one-offs and series with next time, cancel.
4. Log: recent sends from the outbox with status (pending, sent, failed, cancelled), error, time.

Turning the kill switch off cancels every pending outbound outbox row and makes the scheduler skip. The Settings page is untouched.

## 8. Risks left

- The model writes the text. For `ask` contacts the owner sees it before it goes. For `allow` contacts it goes after 15 seconds unless cancelled. That's the trade Zaki chose.
- `allow` plus an owner message that names the contact plus an untainted turn is the only automatic path. A compromised owner account is out of scope.
- WhatsApp ban risk from messaging numbers that never wrote to Palui. Limits help; asking contacts to save Palui's number helps more.

## 9. Implementation plan (each step one commit)

1. `docs`: this document.
2. `feat(db)`: `contacts` and `outbound_messages`, migration.
3. `feat(core)`: outbound service: number normalization, contacts, guards, rate limits, kill switch setting, enqueue with hold, cancel. Tests.
4. `feat(core)`: outbound scheduler extension and outbox worker guards (kill switch at send time, no reroute or fallback). Tests.
5. `feat(core)`: turn passes origin facts to tools; `needsApproval` and `preview` on `PaluiTool`. Tests.
6. `feat(connectors)`: `outbound` connector with the four tools. Tests.
7. `feat(core)`: router undo (`ob:cancel:`, "batal").
8. `feat(core)`: flow `message.contact`. Tests.
9. `feat(app)`: `/app/outbound` page, fixtures.
10. `docs`: changelog and PLAN.

## 10. What shipped

Built on branch `feat/outbound-messages` following section 9, plus per-agent scoping. Two decisions differ slightly from the draft:

- "Now" sends get the undo window even after an explicit approval. It's cheap and catches a wrong tap.
- A one-off time that already passed today is refused instead of rolling to tomorrow, unlike reminders.

## 11. Open questions for Zaki

1. Rate limits: 5 per contact and 20 total per day OK?
2. Signature: append "(dikirim asisten Zaki)" by default, or leave it to the model's wording?
3. Replies from contacts are dropped today. Forward them to you ("Zaki bales: ...")?
4. Quiet hours for flow-triggered sends?
5. Telegram contacts only from existing chats. Fine?
