-- Why an approval was asked (tools.Reason* codes: set-to-ask, tool-rule,
-- destructive, unattended, untrusted, others-wrote, sends-file). Shown to the
-- owner in the prompt and on the dashboard, and to the model when it asks why.
-- NULL on rows from before this migration.
ALTER TABLE `pending_approvals` ADD `reason` text;
