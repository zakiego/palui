CREATE TABLE `routine_occurrences` (
	`id` text PRIMARY KEY NOT NULL,
	`routine_id` text NOT NULL,
	`chat_id` text NOT NULL,
	`key` text NOT NULL,
	`label` text NOT NULL,
	`scheduled_at` integer NOT NULL,
	`fired_at` integer NOT NULL,
	`status` text NOT NULL,
	`answer` text,
	`answered_at` integer,
	`answers` text DEFAULT '[]' NOT NULL,
	`follow_ups` integer DEFAULT 0 NOT NULL,
	`next_follow_up_at` integer,
	`deadline_at` integer,
	`fallback` integer DEFAULT false NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `routine_occ_key` ON `routine_occurrences` (`routine_id`,`key`);--> statement-breakpoint
CREATE INDEX `routine_occ_chat_status` ON `routine_occurrences` (`chat_id`,`status`);--> statement-breakpoint
CREATE INDEX `routine_occ_follow` ON `routine_occurrences` (`status`,`next_follow_up_at`);--> statement-breakpoint
CREATE INDEX `routine_occ_deadline` ON `routine_occurrences` (`status`,`deadline_at`);--> statement-breakpoint
CREATE TABLE `routines` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`name` text NOT NULL,
	`tz` text NOT NULL,
	`days` text,
	`source` text NOT NULL,
	`action` text NOT NULL,
	`recap` text NOT NULL,
	`late_grace_min` integer DEFAULT 60 NOT NULL,
	`enabled` integer DEFAULT true NOT NULL,
	`paused_until` integer,
	`status` text DEFAULT 'idle' NOT NULL,
	`next_fire_at` integer,
	`next_occ` text,
	`cursor` text,
	`source_state` text DEFAULT '{}' NOT NULL,
	`next_refresh_at` integer,
	`next_recap_at` integer,
	`last_error` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `routines_chat` ON `routines` (`chat_id`);--> statement-breakpoint
CREATE INDEX `routines_next` ON `routines` (`enabled`,`status`,`next_fire_at`);