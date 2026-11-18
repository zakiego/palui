-- What the agent's setup looked like on the session's last message turn (Palui
-- version, tools per connector, audience, hashes of persona, notes and chat
-- rules). The next turn diffs against it and tells the model what changed, so
-- it doesn't read a change the owner made as its own earlier mistake.
ALTER TABLE `sessions` ADD `setup_snapshot` text;
