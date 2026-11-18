-- A tool connector can serve several agents: agent_ids is a JSON array of
-- agent ids, and NULL or [] means every agent. agent_id stays and is written
-- as the first id, so a build from before this migration still reads one
-- owner (fewer agents, never more).
ALTER TABLE `connectors` ADD `agent_ids` text;
--> statement-breakpoint
UPDATE `connectors` SET `agent_ids` = json_array(`agent_id`) WHERE `agent_id` IS NOT NULL AND `agent_id` != '';
