CREATE TABLE `attachment_shares` (
	`id` text PRIMARY KEY NOT NULL,
	`attachment_id` text NOT NULL,
	`token_hash` text NOT NULL,
	`visibility` text NOT NULL,
	`created_by` text NOT NULL,
	`expires_at` integer NOT NULL,
	`revoked_at` integer,
	`open_count` integer DEFAULT 0 NOT NULL,
	`last_opened_at` integer,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `attachment_shares_token_hash_unique` ON `attachment_shares` (`token_hash`);--> statement-breakpoint
CREATE INDEX `attachment_shares_attachment` ON `attachment_shares` (`attachment_id`,`created_at`);