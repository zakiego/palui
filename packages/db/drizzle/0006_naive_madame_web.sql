CREATE TABLE `flow_runs` (
	`id` text PRIMARY KEY NOT NULL,
	`flow_id` text NOT NULL,
	`version_id` text NOT NULL,
	`chat_id` text NOT NULL,
	`trigger_id` text,
	`trigger_kind` text NOT NULL,
	`run_key` text NOT NULL,
	`parent_run_id` text,
	`item_index` integer,
	`status` text NOT NULL,
	`skip_reason` text,
	`trigger` text DEFAULT '{}' NOT NULL,
	`vars` text DEFAULT '{}' NOT NULL,
	`taint` text DEFAULT '[]' NOT NULL,
	`cursor` text,
	`wait` text,
	`wake_at` integer,
	`lease_until` integer,
	`steps_done` integer DEFAULT 0 NOT NULL,
	`messages_sent` integer DEFAULT 0 NOT NULL,
	`scheduled_at` integer,
	`started_at` integer NOT NULL,
	`ended_at` integer,
	`error` text,
	`legacy_occurrence_id` text,
	`dry_run` integer DEFAULT false NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `flow_runs_legacy_occurrence_id_unique` ON `flow_runs` (`legacy_occurrence_id`);--> statement-breakpoint
CREATE UNIQUE INDEX `flow_runs_key` ON `flow_runs` (`flow_id`,`run_key`);--> statement-breakpoint
CREATE INDEX `flow_runs_wake` ON `flow_runs` (`status`,`wake_at`);--> statement-breakpoint
CREATE INDEX `flow_runs_chat` ON `flow_runs` (`chat_id`,`status`);--> statement-breakpoint
CREATE INDEX `flow_runs_started` ON `flow_runs` (`flow_id`,`started_at`);--> statement-breakpoint
CREATE INDEX `flow_runs_parent` ON `flow_runs` (`parent_run_id`);--> statement-breakpoint
CREATE TABLE `flow_step_runs` (
	`id` text PRIMARY KEY NOT NULL,
	`run_id` text NOT NULL,
	`seq` integer NOT NULL,
	`step_id` text NOT NULL,
	`kind` text NOT NULL,
	`attempt` integer DEFAULT 1 NOT NULL,
	`status` text NOT NULL,
	`input` text,
	`output` text,
	`tainted` integer DEFAULT false NOT NULL,
	`branch` text,
	`tool_call_id` text,
	`turn_run_id` text,
	`error` text,
	`started_at` integer NOT NULL,
	`ms` integer
);
--> statement-breakpoint
CREATE UNIQUE INDEX `flow_step_runs_seq` ON `flow_step_runs` (`run_id`,`seq`);--> statement-breakpoint
CREATE INDEX `flow_step_runs_started` ON `flow_step_runs` (`started_at`);--> statement-breakpoint
CREATE TABLE `flow_triggers` (
	`id` text PRIMARY KEY NOT NULL,
	`flow_id` text NOT NULL,
	`trigger_id` text NOT NULL,
	`kind` text NOT NULL,
	`match` text,
	`status` text DEFAULT 'idle' NOT NULL,
	`next_fire_at` integer,
	`next_occ` text,
	`cursor` text,
	`source_state` text DEFAULT '{}' NOT NULL,
	`next_refresh_at` integer,
	`poll_state` text DEFAULT '{}' NOT NULL,
	`last_error` text,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `flow_triggers_id` ON `flow_triggers` (`flow_id`,`trigger_id`);--> statement-breakpoint
CREATE INDEX `flow_triggers_match` ON `flow_triggers` (`kind`,`match`);--> statement-breakpoint
CREATE INDEX `flow_triggers_due` ON `flow_triggers` (`status`,`next_fire_at`);--> statement-breakpoint
CREATE TABLE `flow_versions` (
	`id` text PRIMARY KEY NOT NULL,
	`flow_id` text NOT NULL,
	`n` integer NOT NULL,
	`definition` text NOT NULL,
	`hash` text NOT NULL,
	`write_calls` text DEFAULT '[]' NOT NULL,
	`created_by` text NOT NULL,
	`note` text,
	`approved_at` integer,
	`approved_via` text,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `flow_versions_n` ON `flow_versions` (`flow_id`,`n`);--> statement-breakpoint
CREATE TABLE `flows` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`name` text NOT NULL,
	`tz` text NOT NULL,
	`enabled` integer DEFAULT true NOT NULL,
	`paused_until` integer,
	`current_version_id` text,
	`approved_version_id` text,
	`state` text DEFAULT '{"values":{},"tainted":[]}' NOT NULL,
	`consecutive_failures` integer DEFAULT 0 NOT NULL,
	`agent_id` text,
	`legacy_routine_id` text,
	`shadow` integer DEFAULT false NOT NULL,
	`last_error` text,
	`notice_for` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `flows_legacy_routine_id_unique` ON `flows` (`legacy_routine_id`);--> statement-breakpoint
CREATE UNIQUE INDEX `flows_chat_name` ON `flows` (`chat_id`,`name`);