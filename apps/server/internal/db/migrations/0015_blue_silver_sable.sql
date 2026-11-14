ALTER TABLE `chats` ADD `ack` integer DEFAULT true NOT NULL;--> statement-breakpoint
ALTER TABLE `chats` ADD `ack_emoji` text DEFAULT '⏳' NOT NULL;