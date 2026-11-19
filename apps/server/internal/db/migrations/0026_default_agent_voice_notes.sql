-- The owner's own agent transcribes voice notes whenever a voice connector is
-- on. 0024 turned agents on only when a voice connector already existed while
-- it ran, so a connector added right after the upgrade left the default agent
-- off and its voice notes reached the model as "transcription off".
UPDATE `agents` SET `transcribe_voice` = 1 WHERE `is_default` = 1 AND EXISTS (SELECT 1 FROM `connectors` WHERE `type` = 'voice' AND `enabled` = 1);
