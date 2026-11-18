-- Auto-save media: a chat the owner opts in keeps every photo and document
-- sent in it in saved_attachments. chats.save_media is off by default;
-- save_media_days is how long auto-saved files stay (0 keeps them).
-- saved_attachments.origin says who saved a row ('tool' or 'auto'), and the
-- sender columns say who sent the file, since the message row may be gone.
ALTER TABLE `chats` ADD `save_media` integer DEFAULT false NOT NULL;--> statement-breakpoint
ALTER TABLE `chats` ADD `save_media_days` integer DEFAULT 30 NOT NULL;--> statement-breakpoint
ALTER TABLE `saved_attachments` ADD `origin` text DEFAULT 'tool' NOT NULL;--> statement-breakpoint
ALTER TABLE `saved_attachments` ADD `sender_id` text;--> statement-breakpoint
ALTER TABLE `saved_attachments` ADD `sender_name` text;--> statement-breakpoint
ALTER TABLE `saved_attachments` ADD `sent_at` integer;--> statement-breakpoint
CREATE INDEX `saved_attachments_origin` ON `saved_attachments` (`origin`,`chat_id`,`created_at`);
