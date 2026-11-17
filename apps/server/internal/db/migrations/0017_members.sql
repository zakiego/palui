-- Members (PLAN 19.4): people the owner shares an agent with. Their channel
-- accounts live in member_accounts, never in identities, which stays owner
-- only. agent_members links a member to the agents they share. New tables
-- only, no foreign keys. See internal/members.
CREATE TABLE `members` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE TABLE `member_accounts` (
	`id` text PRIMARY KEY NOT NULL,
	`member_id` text NOT NULL,
	`channel` text NOT NULL,
	`external_id` text NOT NULL,
	`alt_id` text,
	`display_name` text,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `member_accounts_channel_external` ON `member_accounts` (`channel`,`external_id`);
--> statement-breakpoint
CREATE INDEX `member_accounts_alt` ON `member_accounts` (`channel`,`alt_id`);
--> statement-breakpoint
CREATE INDEX `member_accounts_member` ON `member_accounts` (`member_id`);
--> statement-breakpoint
CREATE TABLE `agent_members` (
	`agent_id` text NOT NULL,
	`member_id` text NOT NULL,
	`created_at` integer NOT NULL,
	PRIMARY KEY(`agent_id`, `member_id`)
);
--> statement-breakpoint
CREATE INDEX `agent_members_member` ON `agent_members` (`member_id`);
