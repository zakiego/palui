CREATE TABLE `account` (
	`id` text PRIMARY KEY NOT NULL,
	`account_id` text NOT NULL,
	`provider_id` text NOT NULL,
	`user_id` text NOT NULL,
	`access_token` text,
	`refresh_token` text,
	`id_token` text,
	`access_token_expires_at` integer,
	`refresh_token_expires_at` integer,
	`scope` text,
	`password` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `account_user_id_idx` ON `account` (`user_id`);--> statement-breakpoint
CREATE TABLE `chats` (
	`id` text PRIMARY KEY NOT NULL,
	`channel` text NOT NULL,
	`external_id` text NOT NULL,
	`title` text DEFAULT '' NOT NULL,
	`kind` text DEFAULT 'dm' NOT NULL,
	`session_id` text,
	`allowed` integer DEFAULT true NOT NULL,
	`last_message_at` integer,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `chats_channel_external` ON `chats` (`channel`,`external_id`);--> statement-breakpoint
CREATE TABLE `claims` (
	`nonce` text PRIMARY KEY NOT NULL,
	`channel` text NOT NULL,
	`expires_at` integer NOT NULL,
	`used_at` integer,
	`used_by` text,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE TABLE `connectors` (
	`id` text PRIMARY KEY NOT NULL,
	`type` text NOT NULL,
	`kind` text NOT NULL,
	`label` text NOT NULL,
	`enabled` integer DEFAULT false NOT NULL,
	`status` text DEFAULT 'not_set_up' NOT NULL,
	`status_detail` text,
	`trust` text DEFAULT 'own' NOT NULL,
	`config` text DEFAULT '{}' NOT NULL,
	`secrets` text,
	`config_hash` text DEFAULT '' NOT NULL,
	`error_since` integer,
	`alerted_at` integer,
	`sort_order` integer DEFAULT 0 NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE TABLE `events` (
	`id` integer PRIMARY KEY AUTOINCREMENT NOT NULL,
	`at` integer NOT NULL,
	`level` text NOT NULL,
	`connector_id` text,
	`kind` text NOT NULL,
	`message` text NOT NULL,
	`data` text
);
--> statement-breakpoint
CREATE INDEX `events_at` ON `events` (`at`);--> statement-breakpoint
CREATE INDEX `events_connector` ON `events` (`connector_id`,`at`);--> statement-breakpoint
CREATE TABLE `hooks` (
	`id` text PRIMARY KEY NOT NULL,
	`slug` text NOT NULL,
	`name` text NOT NULL,
	`secret` text NOT NULL,
	`mode` text DEFAULT 'forward' NOT NULL,
	`chat_id` text NOT NULL,
	`enabled` integer DEFAULT true NOT NULL,
	`last_at` integer,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `hooks_slug_unique` ON `hooks` (`slug`);--> statement-breakpoint
CREATE TABLE `identities` (
	`id` text PRIMARY KEY NOT NULL,
	`channel` text NOT NULL,
	`external_id` text NOT NULL,
	`alt_id` text,
	`display_name` text,
	`role` text DEFAULT 'owner' NOT NULL,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `identities_channel_external` ON `identities` (`channel`,`external_id`);--> statement-breakpoint
CREATE INDEX `identities_alt` ON `identities` (`channel`,`alt_id`);--> statement-breakpoint
CREATE TABLE `jobs` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`kind` text DEFAULT 'job' NOT NULL,
	`rrule` text NOT NULL,
	`tz` text NOT NULL,
	`local_start` text NOT NULL,
	`prompt` text NOT NULL,
	`chat_id` text NOT NULL,
	`model_role` text DEFAULT 'default' NOT NULL,
	`tool_scope` text,
	`enabled` integer DEFAULT true NOT NULL,
	`next_run_at` integer,
	`status` text DEFAULT 'idle' NOT NULL,
	`last_run_at` integer,
	`last_status` text,
	`last_result` text,
	`daily_token_cap` integer DEFAULT 50000 NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `jobs_next` ON `jobs` (`enabled`,`next_run_at`);--> statement-breakpoint
CREATE TABLE `messages` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`session_id` text,
	`role` text NOT NULL,
	`content` text NOT NULL,
	`text` text DEFAULT '' NOT NULL,
	`channel_msg_id` text,
	`reply_to_channel_msg_id` text,
	`sender_id` text,
	`status` text DEFAULT 'handled' NOT NULL,
	`ignore_reason` text,
	`run_id` text,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `messages_chat_created` ON `messages` (`chat_id`,`created_at`);--> statement-breakpoint
CREATE INDEX `messages_status` ON `messages` (`status`);--> statement-breakpoint
CREATE INDEX `messages_session` ON `messages` (`session_id`,`created_at`);--> statement-breakpoint
CREATE TABLE `outbox` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`channel` text NOT NULL,
	`payload` text NOT NULL,
	`status` text DEFAULT 'pending' NOT NULL,
	`attempts` integer DEFAULT 0 NOT NULL,
	`next_attempt_at` integer NOT NULL,
	`claimed_at` integer,
	`channel_msg_id` text,
	`source` text NOT NULL,
	`error` text,
	`fallback_from` text,
	`created_at` integer NOT NULL,
	`expires_at` integer,
	`sent_at` integer
);
--> statement-breakpoint
CREATE INDEX `outbox_status_next` ON `outbox` (`status`,`next_attempt_at`);--> statement-breakpoint
CREATE INDEX `outbox_chat` ON `outbox` (`chat_id`);--> statement-breakpoint
CREATE TABLE `owner_invites` (
	`id` text PRIMARY KEY NOT NULL,
	`token_hash` text NOT NULL,
	`expires_at` integer NOT NULL,
	`used_at` integer,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `owner_invites_token_hash_unique` ON `owner_invites` (`token_hash`);--> statement-breakpoint
CREATE TABLE `passkey` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text,
	`public_key` text NOT NULL,
	`user_id` text NOT NULL,
	`credential_id` text NOT NULL,
	`counter` integer NOT NULL,
	`device_type` text NOT NULL,
	`backed_up` integer NOT NULL,
	`transports` text,
	`created_at` integer,
	`aaguid` text,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `passkey_credential_id_unique` ON `passkey` (`credential_id`);--> statement-breakpoint
CREATE INDEX `passkey_user_id_idx` ON `passkey` (`user_id`);--> statement-breakpoint
CREATE TABLE `pending_approvals` (
	`id` text PRIMARY KEY NOT NULL,
	`run_id` text NOT NULL,
	`chat_id` text NOT NULL,
	`approval_id` text NOT NULL,
	`tool_call_id` text,
	`tool` text NOT NULL,
	`tool_class` text NOT NULL,
	`args_preview` text,
	`messages` text NOT NULL,
	`context` text DEFAULT '{}' NOT NULL,
	`prompt_msg_id` text,
	`expires_at` integer NOT NULL,
	`status` text DEFAULT 'pending' NOT NULL,
	`decided_at` integer,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `approvals_chat_status` ON `pending_approvals` (`chat_id`,`status`);--> statement-breakpoint
CREATE INDEX `approvals_status_expires` ON `pending_approvals` (`status`,`expires_at`);--> statement-breakpoint
CREATE TABLE `reminders` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`text` text NOT NULL,
	`rrule` text,
	`tz` text NOT NULL,
	`local_start` text NOT NULL,
	`fire_at` integer NOT NULL,
	`scheduled_for` integer NOT NULL,
	`status` text DEFAULT 'pending' NOT NULL,
	`last_fired_at` integer,
	`snoozed_until` integer,
	`smart_prompt` text,
	`last_outbox_id` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `reminders_status_fire` ON `reminders` (`status`,`fire_at`);--> statement-breakpoint
CREATE INDEX `reminders_chat` ON `reminders` (`chat_id`);--> statement-breakpoint
CREATE TABLE `runs` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text,
	`job_id` text,
	`trigger` text NOT NULL,
	`model` text,
	`model_connector_id` text,
	`tokens_in` integer DEFAULT 0 NOT NULL,
	`tokens_out` integer DEFAULT 0 NOT NULL,
	`tokens_cached` integer DEFAULT 0 NOT NULL,
	`steps` integer DEFAULT 0 NOT NULL,
	`tainted` integer DEFAULT false NOT NULL,
	`status` text DEFAULT 'running' NOT NULL,
	`error` text,
	`result_preview` text,
	`started_at` integer NOT NULL,
	`ms` integer
);
--> statement-breakpoint
CREATE INDEX `runs_started` ON `runs` (`started_at`);--> statement-breakpoint
CREATE INDEX `runs_job` ON `runs` (`job_id`,`started_at`);--> statement-breakpoint
CREATE TABLE `session` (
	`id` text PRIMARY KEY NOT NULL,
	`expires_at` integer NOT NULL,
	`token` text NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL,
	`ip_address` text,
	`user_agent` text,
	`user_id` text NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `session_token_unique` ON `session` (`token`);--> statement-breakpoint
CREATE INDEX `session_user_id_idx` ON `session` (`user_id`);--> statement-breakpoint
CREATE TABLE `sessions` (
	`id` text PRIMARY KEY NOT NULL,
	`chat_id` text NOT NULL,
	`summary` text,
	`summarized_until` integer,
	`saved_to_recall_at` integer,
	`created_at` integer NOT NULL,
	`closed_at` integer
);
--> statement-breakpoint
CREATE INDEX `sessions_chat` ON `sessions` (`chat_id`);--> statement-breakpoint
CREATE TABLE `settings` (
	`key` text PRIMARY KEY NOT NULL,
	`value` text NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE TABLE `tool_calls` (
	`id` text PRIMARY KEY NOT NULL,
	`run_id` text NOT NULL,
	`connector_id` text,
	`tool` text NOT NULL,
	`class` text NOT NULL,
	`args` text,
	`result_preview` text,
	`status` text NOT NULL,
	`ms` integer,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `tool_calls_run` ON `tool_calls` (`run_id`);--> statement-breakpoint
CREATE INDEX `tool_calls_created` ON `tool_calls` (`created_at`);--> statement-breakpoint
CREATE TABLE `usage` (
	`key` text PRIMARY KEY NOT NULL,
	`value` integer DEFAULT 0 NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE TABLE `user` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`email` text NOT NULL,
	`email_verified` integer DEFAULT false NOT NULL,
	`image` text,
	`username` text,
	`display_username` text,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `user_email_unique` ON `user` (`email`);--> statement-breakpoint
CREATE UNIQUE INDEX `user_username_unique` ON `user` (`username`);--> statement-breakpoint
CREATE TABLE `verification` (
	`id` text PRIMARY KEY NOT NULL,
	`identifier` text NOT NULL,
	`value` text NOT NULL,
	`expires_at` integer NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `verification_identifier_idx` ON `verification` (`identifier`);