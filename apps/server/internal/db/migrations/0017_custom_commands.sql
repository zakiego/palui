-- Custom chat commands (PLAN 8.3): the owner saves a prompt under a /name in
-- the dashboard, for every agent (agent_id NULL) or one agent. Typing the
-- name runs a normal turn with that prompt. New table only.
CREATE TABLE `custom_commands` (
	`id` text PRIMARY KEY NOT NULL,
	`agent_id` text,
	`name` text NOT NULL,
	`description` text DEFAULT '' NOT NULL,
	`prompt` text NOT NULL,
	`attach_report` integer DEFAULT 0 NOT NULL,
	`enabled` integer DEFAULT 1 NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL,
	FOREIGN KEY (`agent_id`) REFERENCES `agents`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `custom_commands_scope_name_unique` ON `custom_commands` (ifnull(`agent_id`, ''), `name`);
