# 0002: @mentions in WhatsApp groups

Status: built, 2026-11-22. Debug report: `rpt_0mwrwosczfzc3p58slq`.

## Context

Asked in a WhatsApp group whether it could tag people, an agent said it could only write names as plain text. That was true. Outgoing WhatsApp messages were a bare `Conversation` (or an `ExtendedTextMessage` only to quote), with no `ContextInfo.MentionedJID`, and the model had no way to name a person's JID.

A WhatsApp mention is two things that must agree: the text holds `@<user part of the JID>`, and that JID is in `ContextInfo.MentionedJID`. The app then shows the person's name in place of the digits and notifies them. Groups now use LID addressing (`<digits>@lid`), so the user part is a LID, not a phone number.

## Decision

1. The agent writes WhatsApp's own token, `@<id>`, in its reply. No new marker syntax and no new tool: the token is already how mentions look in inbound text, so the history the model reads and the replies it writes use one format.
2. For WhatsApp group turns, the system prompt lists the people the agent can mention: `Name: @<id>`. A person is listed when they are in the group right now (the channel's `GroupParticipants`, cached 10 minutes) and have written in the chat, so there is a name to ask for them by (their latest `messages.sender_name`). The id is the user part of the participant's main JID, which follows the group's addressing (a LID in LID groups). At most 40 people, most recent writers first. Names are marked as data, never instructions. Hook turns get no list.
3. The WhatsApp channel resolves tokens at send time (`whatsapp.ResolveMentions` in `(*wa).Send`). A token becomes a mention only when its digits are the user part of a current participant's LID or phone JID, and the mentioned JID keeps the form the token used, so text and JID always line up. Unknown tokens, names written as `@Name`, and everything when the participant list can't be read go out as plain text. A JID is never made up.
4. At most 10 mentions per outbox message (`whatsapp.MaxMentions`). Tokens past that stay plain text. There is no "tag everyone".
5. Split messages carry only the mentions whose token is in that chunk. Media captions carry mentions the same way.
6. `db.OutboxPayload` and the outbox worker are unchanged. Mentions live in the text, and only the WhatsApp channel reads them.

## Consequences

- Every path that sends to a WhatsApp group gets mentions for free, since they all go through the outbox and `(*wa).Send`: agent replies, reminders, flow message steps, outbound messages and system notices. Only an agent turn knows the ids from the prompt list, so in practice the text comes from an agent (a reminder it scheduled, a flow it drafted). DMs never look anything up.
- Hook messages in `forward` mode are sent as is, so a webhook text holding a participant's id would mention them. The participant check and the cap bound that. Hook `summarize` turns get no list.
- Telegram is unchanged. It gets no list, and an `@123` in its text is plain text.
- A participant who never wrote in the chat can't be mentioned by name. That avoids guessing who someone is from a bare id.
- The send may wait up to 5 s on a participant lookup when the cache is cold and the text has a token.
- The system prompt changes when someone new writes in the group, which costs a cache write on the stable system block for that turn.

## Alternatives

- A structured marker the channel rewrites, like `@[Name]`: one more syntax for the model to learn and for history to explain, and a name-to-JID match at send time is ambiguous for shared or changed names.
- A `mention` parameter on a send tool: replies aren't sent through a tool, so the reply path would need one more round trip, and other paths would still lack it.
- Storing a group roster table: the participant list is already read for the audience (PLAN 19.4) and cached; names come from messages. A table can wait until a dashboard needs it.
