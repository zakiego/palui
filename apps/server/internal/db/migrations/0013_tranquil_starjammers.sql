ALTER TABLE `agents` ADD `recall_read_containers` text;--> statement-breakpoint
ALTER TABLE `agents` ADD `recall_per_chat` integer DEFAULT false NOT NULL;