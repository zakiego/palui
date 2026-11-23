# 0003: Keep group photos for a day and show quoted ones to the agent

Status: built, 2026-11-23. Debug report: `rpt_0mwsqf7y5vjh0reoc1a`.

## Context

In a WhatsApp group, a member sent an album of photos as group talk. The owner then replied to it and mentioned the agent ("in apa"). The agent answered that it couldn't read the quoted message or see the photo.

Two things were missing:

1. **The quote was empty.** WhatsApp sends an album as one `albumMessage` (counts only, no text or media), then each photo as its own message whose `messageContextInfo.messageAssociation` points back at the album's id. A reply to the album quotes the album message. Palui dropped that message as empty, never stored it, and had no idea which photos belonged to it, so the quote resolved to `{from: other, text: ""}` and the prompt said the quoted message was "no longer available".
2. **The photos had no bytes.** PLAN 19 said group talk kept as context loses its media bytes. The channel didn't even download them (`Services.WantsMedia` is false for context). The channel's own quoted-photo fetch (a reply to a single photo, using the media keys inside the quote) can't help with an album, because the album message carries no media keys. A photo in the same album as the one that mentioned the agent also showed only as `[gambar dari sebelumnya]`.

The earlier fix for `rpt_0mwmwtby5l0zrpy3sgg` made group talk photos say who sent them. They stayed placeholders, so this is a gap it left, not a regression.

## Decision

1. **Albums.** The WhatsApp channel reads the album id from `messageAssociation` (`whatsapp.AlbumOf`) and passes it as `InboundMessage.AlbumID`. It's stored as `content.album`. A quoted album message is labeled `[album of N]`. When a reply's stanza id matches no stored message, the quote lookup checks stored rows of that album in the same chat (`chat.albumQuote`): the first caption, who sent it, and `[album of N photos]`.
2. **Group talk keeps its photos for 24 hours.** In a group the sender check keeps as context (allowed, with an agent), the channel downloads a photo (not a sticker, document, voice note or video) up to `connectors.ContextPhotoMaxBytes` (5 MB), through the new optional `Services.KeepsMedia`. The engine keeps those bytes on the context row. `PruneContextMedia` clears them after `ContextMediaKeepMs` (24 hours), checked hourly from the existing sweep, and leaves the entry with `note: "expired"` so a later reply can say why it's gone. Auto-save (`chats.save_media`) is unchanged and still the only way to keep a file longer.
3. **The model sees a kept photo only when a batch message points at it.** At turn time, `AttachQuotedImages` attaches, per batch message: the photo(s) it replies to (by channel message id, else by album), unless the message already carries its own photo; and the other photos of the album it came in. At most `MaxAttachedImages` (4) per turn. Each set comes after a note (`[the quoted photo is attached]`, `[2 more photos from the same album attached]`). History rows still never carry images, and the vision role is chosen when anything is attached.
4. **Honest markers.** A quoted photo whose bytes expired says `[the quoted photo can't be shown: photos from group talk are kept for 24 hours and this is older]`. One that was never downloaded says so. Photos past the cap are counted. A quote whose content never reached Palui now reads `[Replying to ... Its content didn't reach you, so you can't see what it said]` instead of "no longer available". The group system prompt tells the agent to pass those reasons on as they are.
5. **Same chat only.** Every lookup is scoped by `chat_id`. A DM that quotes a group message's id finds nothing.

## Consequences

- The case in the report works: the owner's reply to another member's album gets the album's caption, sender and photos.
- Kept photos also show up for 24 hours in the `earlier: true` file lookup of the attachments and printer tools (`attachments.RecentCandidates` reads any row with bytes in the same chat). Those tools already only see the current chat, and saving still goes through the tool's own rules.
- The database holds group photos for up to a day. WhatsApp photos are compressed (usually under 500 KB), and only allowed groups with an agent keep them.
- The dashboard's chat view reads the same rows, so the owner can see those photos there for that day.
- When the mention arrives a moment before the rest of its album, the turn may start before those photos are stored. They then show only as placeholders in history, as before.
- Telegram is unchanged at the channel: it doesn't keep group talk photos. A Telegram reply to a stored photo that still has its bytes gets it attached the same way.

## Alternatives

- **Keep the WhatsApp media keys instead of the bytes, and download on demand.** Smaller rows, but the keys are as sensitive as the photo, WhatsApp's CDN links expire on their own schedule, and the channel would need a second lookup path from the engine back to the socket.
- **An in-memory cache of recent group photos.** Lost on every restart, and the server restarted between the photo and the reply in this very report.
- **Opt-in per chat.** Group text is already stored without an opt-in, and 24 hours is shorter than any auto-save retention. A per-chat switch can be added later if a group asks for it.
- **Download only when quoted.** The album message carries no keys, so there is nothing to download from at reply time.
