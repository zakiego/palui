-- Replies to proactive messages are now saved as their own kind of note
-- (source 'reply', expiring after 3 days) instead of permanent owner notes.
-- Owner notes that an earlier build saved from a reply, in its
-- `About "<message>": <reply>` form, become reply notes too. Nothing is
-- deleted and expires_at stays NULL: these legacy notes never expire on their
-- own and are never pushed out when memory is full. They only show up as
-- replies on the Proactive tab, where the owner can keep or delete them.
UPDATE `proactive_notes` SET `source` = 'reply' WHERE `source` = 'owner' AND `expires_at` IS NULL AND `text` GLOB 'About "*": *';
