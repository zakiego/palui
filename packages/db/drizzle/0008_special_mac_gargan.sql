CREATE TABLE `reports` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`session_id` text,
	`note` text DEFAULT '' NOT NULL,
	`text` text NOT NULL,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `reports_chat` ON `reports` (`chat_id`,`created_at`);