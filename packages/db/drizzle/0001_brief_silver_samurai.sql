CREATE TABLE `saved_attachments` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`message_id` text,
	`run_id` text,
	`title` text NOT NULL,
	`description` text DEFAULT '' NOT NULL,
	`kind` text NOT NULL,
	`mime_type` text NOT NULL,
	`filename` text,
	`size` integer NOT NULL,
	`sha256` text NOT NULL,
	`data` blob NOT NULL,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `saved_attachments_chat_created` ON `saved_attachments` (`chat_id`,`created_at`);--> statement-breakpoint
CREATE INDEX `saved_attachments_chat_sha` ON `saved_attachments` (`chat_id`,`sha256`);