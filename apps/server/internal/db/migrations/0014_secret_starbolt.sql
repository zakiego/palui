CREATE TABLE `contacts` (
	`id` text PRIMARY KEY NOT NULL,
	`agent_id` text,
	`name` text NOT NULL,
	`channel` text NOT NULL,
	`address` text NOT NULL,
	`chat_id` text NOT NULL,
	`policy` text DEFAULT 'ask' NOT NULL,
	`note` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL,
	`removed_at` integer
);
--> statement-breakpoint
CREATE INDEX `contacts_agent` ON `contacts` (`agent_id`,`removed_at`);--> statement-breakpoint
CREATE INDEX `contacts_chat` ON `contacts` (`chat_id`);--> statement-breakpoint
CREATE TABLE `outbound_messages` (
	`id` text PRIMARY KEY NOT NULL,
	`agent_id` text,
	`contact_id` text NOT NULL,
	`text` text NOT NULL,
	`origin` text NOT NULL,
	`origin_ref` text,
	`approved_by` text NOT NULL,
	`owner_chat_id` text,
	`rrule` text,
	`tz` text NOT NULL,
	`local_start` text NOT NULL,
	`fire_at` integer NOT NULL,
	`status` text DEFAULT 'scheduled' NOT NULL,
	`last_outbox_id` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `outbound_status_fire` ON `outbound_messages` (`status`,`fire_at`);--> statement-breakpoint
CREATE INDEX `outbound_contact` ON `outbound_messages` (`contact_id`);