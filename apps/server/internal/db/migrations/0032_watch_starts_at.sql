-- When a watch starts checking (Unix ms). Before it, the watch isn't checked at
-- all: its first next_check_at is this time. NULL means it started when it was
-- made, which is every row from before this migration.
ALTER TABLE `watches` ADD `starts_at` integer;
