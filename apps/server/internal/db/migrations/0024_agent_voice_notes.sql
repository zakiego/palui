-- Voice notes are transcribed per agent, off by default. Agents that existed
-- while a voice connector was already on keep transcribing, so an upgrade
-- doesn't quietly turn off something the owner set up.
ALTER TABLE `agents` ADD `transcribe_voice` integer DEFAULT false NOT NULL;
--> statement-breakpoint
UPDATE `agents` SET `transcribe_voice` = 1 WHERE EXISTS (SELECT 1 FROM `connectors` WHERE `type` = 'voice' AND `enabled` = 1);
