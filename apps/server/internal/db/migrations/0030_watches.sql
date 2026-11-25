-- Watches (internal/watches, PLAN 6e): someone in a chat asks the agent to
-- keep an eye on something ("kabarin kalau laba resmi Danantara udah
-- keluar"). Every every_min minutes a read-only turn checks it; the first
-- time the condition is met it posts once to the chat and the watch is done.
-- It never outlives expires_at. Status: active, checking (claimed, lease in
-- lease_until), done, expired, cancelled or failed. notes is the model's own
-- memory between checks (what it already saw), last_summary one line on
-- where things stand, sent with the expiry notice.
CREATE TABLE `watches` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`agent_id` text,
	`created_by` text DEFAULT '' NOT NULL,
	`topic` text NOT NULL,
	`question` text NOT NULL,
	`done_when` text NOT NULL,
	`every_min` integer NOT NULL,
	`expires_at` integer NOT NULL,
	`status` text DEFAULT 'active' NOT NULL,
	`next_check_at` integer,
	`lease_until` integer,
	`checks` integer DEFAULT 0 NOT NULL,
	`fails` integer DEFAULT 0 NOT NULL,
	`last_check_at` integer,
	`last_run_id` text,
	`last_error` text,
	`notes` text DEFAULT '' NOT NULL,
	`last_summary` text DEFAULT '' NOT NULL,
	`tokens_used` integer DEFAULT 0 NOT NULL,
	`token_cap` integer NOT NULL,
	`result` text,
	`outbox_id` text,
	`ended_at` integer,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `watches_due` ON `watches` (`status`,`next_check_at`);
--> statement-breakpoint
CREATE INDEX `watches_chat` ON `watches` (`chat_id`,`status`);
