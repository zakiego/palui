CREATE TABLE `agents` (
	`id` text PRIMARY KEY NOT NULL,
	`slug` text NOT NULL,
	`name` text NOT NULL,
	`persona` text NOT NULL,
	`is_default` integer DEFAULT false NOT NULL,
	`recall_container` text,
	`owner_powers` text DEFAULT 'off' NOT NULL,
	`guest_tools` text DEFAULT 'safe' NOT NULL,
	`daily_token_cap` integer DEFAULT 0 NOT NULL,
	`telegram_connector_id` text,
	`avatar_image` text,
	`avatar_icon` text,
	`avatar_color` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `agents_slug_unique` ON `agents` (`slug`);--> statement-breakpoint
CREATE UNIQUE INDEX `agents_telegram_connector_id_unique` ON `agents` (`telegram_connector_id`);--> statement-breakpoint
DROP INDEX `chats_channel_external`;--> statement-breakpoint
ALTER TABLE `chats` ADD `connector_id` text DEFAULT '' NOT NULL;--> statement-breakpoint
ALTER TABLE `chats` ADD `agent_id` text;--> statement-breakpoint
CREATE UNIQUE INDEX `chats_channel_connector_external` ON `chats` (`channel`,`connector_id`,`external_id`);--> statement-breakpoint
ALTER TABLE `instructions` ADD `agent_id` text;--> statement-breakpoint
ALTER TABLE `messages` ADD `sender_name` text;--> statement-breakpoint
ALTER TABLE `messages` ADD `from_owner` integer DEFAULT true NOT NULL;--> statement-breakpoint
ALTER TABLE `outbox` ADD `connector_id` text;--> statement-breakpoint
ALTER TABLE `pending_approvals` ADD `ask_chat_id` text;--> statement-breakpoint
ALTER TABLE `runs` ADD `agent_id` text;