-- Outbound media: the bytes of a photo, file, voice note, video or sticker an
-- agent sends to a chat. An outbox row points at one through
-- payload.media.id; the bytes are encrypted with MASTER_KEY like
-- saved_attachments. A row is kept while an outbox row that points at it can
-- still be sent, then swept. source is 'url' or 'attachment', source_ref the
-- URL or the attachment id it came from.
CREATE TABLE `outbox_media` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`kind` text NOT NULL,
	`mime_type` text NOT NULL,
	`filename` text,
	`size` integer NOT NULL,
	`sha256` text NOT NULL,
	`source` text NOT NULL,
	`source_ref` text,
	`data` blob NOT NULL,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `outbox_media_created` ON `outbox_media` (`created_at`);
