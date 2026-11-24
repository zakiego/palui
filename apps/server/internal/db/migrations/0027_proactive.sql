-- Proactive (docs/rfc/proactive.md): an agent checks the owner's calendar and
-- WhatsApp on a timer and tells the owner what matters, without being asked.
-- agent_proactive is one row per agent (off until the owner turns it on);
-- config is versioned JSON. nudges is the ledger: one row per thing the agent
-- found (key unique per agent), so nothing is told twice. proactive_mutes are
-- subjects the owner said not to be reminded of again. proactive_ticks logs
-- each collection pass for the activity list (kept 30 days).
CREATE TABLE `agent_proactive` (
	`agent_id` text PRIMARY KEY NOT NULL,
	`enabled` integer DEFAULT false NOT NULL,
	`chat_id` text,
	`config` text NOT NULL,
	`next_tick_at` integer,
	`last_tick_at` integer,
	`tick_lease_until` integer,
	`paused_reason` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE TABLE `nudges` (
	`id` text PRIMARY KEY NOT NULL,
	`agent_id` text NOT NULL,
	`key` text NOT NULL,
	`kind` text NOT NULL,
	`subject` text NOT NULL,
	`status` text NOT NULL,
	`wake_at` integer,
	`sent_at` integer,
	`escalated_at` integer,
	`snapshot` text NOT NULL,
	`reason` text,
	`outbox_id` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `nudges_agent_key` ON `nudges` (`agent_id`,`key`);
--> statement-breakpoint
CREATE INDEX `nudges_status_wake` ON `nudges` (`status`,`wake_at`);
--> statement-breakpoint
CREATE INDEX `nudges_outbox` ON `nudges` (`outbox_id`);
--> statement-breakpoint
CREATE TABLE `proactive_mutes` (
	`agent_id` text NOT NULL,
	`subject` text NOT NULL,
	`label` text NOT NULL,
	`created_at` integer NOT NULL,
	PRIMARY KEY(`agent_id`, `subject`)
);
--> statement-breakpoint
CREATE TABLE `proactive_ticks` (
	`id` text PRIMARY KEY NOT NULL,
	`agent_id` text NOT NULL,
	`started_at` integer NOT NULL,
	`finished_at` integer,
	`sources` text NOT NULL,
	`found` integer DEFAULT 0 NOT NULL,
	`sent` integer DEFAULT 0 NOT NULL,
	`tokens` integer DEFAULT 0 NOT NULL,
	`error` text
);
--> statement-breakpoint
CREATE INDEX `proactive_ticks_agent_started` ON `proactive_ticks` (`agent_id`,`started_at`);
