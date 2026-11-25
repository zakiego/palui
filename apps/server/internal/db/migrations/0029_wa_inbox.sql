-- WhatsApp inbox (connectors/wainbox, docs/rfc/proactive-prompt.md 10): the
-- owner's own number linked read-only. One row per chat with only what
-- "waiting for a reply" needs: the chat's name and kind, when the last
-- message came in, when the owner last replied, since when it has been
-- waiting, how many messages wait, and a 200-character preview. Message
-- bodies are never kept.
CREATE TABLE `wa_inbox_chats` (
	`connector_id` text NOT NULL,
	`chat_jid` text NOT NULL,
	`kind` text NOT NULL,
	`name` text DEFAULT '' NOT NULL,
	`last_in_at` integer,
	`last_out_at` integer,
	`waiting_since` integer,
	`unanswered` integer DEFAULT 0 NOT NULL,
	`preview` text DEFAULT '' NOT NULL,
	`updated_at` integer NOT NULL,
	PRIMARY KEY(`connector_id`, `chat_jid`)
);
--> statement-breakpoint
CREATE INDEX `wa_inbox_chats_waiting` ON `wa_inbox_chats` (`connector_id`,`waiting_since`);
