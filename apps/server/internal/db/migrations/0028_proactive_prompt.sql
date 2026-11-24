-- Proactive from a prompt (docs/rfc/proactive-prompt.md): the owner writes one
-- prompt that mentions the read tools it may use, instead of picking fixed
-- calendar and WhatsApp sources. proactive_notes is its memory between runs:
-- short notes the model or the owner wrote (owner notes never expire).
-- Open nudges from the fixed sources are closed, and an agent that had
-- proactive on is paused until the owner writes a prompt.
CREATE TABLE `proactive_notes` (
	`id` text PRIMARY KEY NOT NULL,
	`agent_id` text NOT NULL,
	`text` text NOT NULL,
	`source` text NOT NULL,
	`expires_at` integer,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL
);
--> statement-breakpoint
CREATE INDEX `proactive_notes_agent` ON `proactive_notes` (`agent_id`,`created_at`);
--> statement-breakpoint
UPDATE `nudges` SET `status` = 'dropped', `reason` = 'proactive now runs from a prompt', `wake_at` = NULL, `updated_at` = CAST(strftime('%s', 'now') AS INTEGER) * 1000 WHERE `status` IN ('pending', 'scheduled', 'snoozed', 'firing', 'tracking') AND `kind` NOT IN ('sum_morning', 'sum_evening');
--> statement-breakpoint
UPDATE `agent_proactive` SET `paused_reason` = 'Proactive now runs from a prompt. Write one on the Proactive tab, mention the tools it may read, and save.', `next_tick_at` = NULL WHERE `enabled` = 1;
