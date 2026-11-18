-- Chat rules: short rules for one shared chat that the owner lets chosen
-- members add from that chat. They live apart from instructions, which stay
-- the owner's and apply to every chat of the agent. agent_members.can_set_rules
-- is the owner's opt-in per member (off by default). chat_rule_changes keeps
-- every add and remove, who made it, and whether the owner undid it.
ALTER TABLE `agent_members` ADD `can_set_rules` integer DEFAULT false NOT NULL;
--> statement-breakpoint
CREATE TABLE `chat_rules` (
	`id` text PRIMARY KEY NOT NULL,
	`agent_id` text NOT NULL,
	`chat_id` text NOT NULL,
	`title` text NOT NULL,
	`body` text NOT NULL,
	`member_id` text,
	`author_name` text NOT NULL,
	`created_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `chat_rules_chat` ON `chat_rules` (`agent_id`,`chat_id`,`created_at`);
--> statement-breakpoint
CREATE TABLE `chat_rule_changes` (
	`id` text PRIMARY KEY NOT NULL,
	`agent_id` text NOT NULL,
	`chat_id` text NOT NULL,
	`rule_id` text NOT NULL,
	`action` text NOT NULL,
	`title` text NOT NULL,
	`body` text NOT NULL,
	`member_id` text,
	`author_name` text NOT NULL,
	`by_member_id` text,
	`by_name` text NOT NULL,
	`run_id` text,
	`created_at` integer NOT NULL,
	`undone_at` integer
);
--> statement-breakpoint
CREATE INDEX `chat_rule_changes_agent` ON `chat_rule_changes` (`agent_id`,`created_at`);
