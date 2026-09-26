-- When a selector was last re-recorded (`automation rerecord`). Re-recording
-- resets the row's counters and outcome ring (automation.ResetSelectorHealth),
-- so doctor reports the new selector as ok until fresh failures arrive.
ALTER TABLE automation_selector_health ADD COLUMN rerecorded_at TEXT;
