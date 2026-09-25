package automation

import (
	"database/sql"
	"time"
)

const healthResetSQL = `
INSERT INTO automation_selector_health
    (automation_id, selector_key, ok_count, fail_count, healed_count,
     last_ok_at, last_fail_at, last_candidate_index, recent, rerecorded_at, updated_at)
VALUES (?, ?, 0, 0, 0, NULL, NULL, -1, '', ?, ?)
ON CONFLICT(automation_id, selector_key) DO UPDATE SET
    ok_count = 0, fail_count = 0, healed_count = 0,
    last_ok_at = NULL, last_fail_at = NULL, last_candidate_index = -1,
    recent = '', rerecorded_at = excluded.rerecorded_at, updated_at = excluded.updated_at`

// ResetSelectorHealth starts a selector's health over after it was
// re-recorded (`automation rerecord`): counters, last ok/fail and the
// outcome ring are cleared and rerecorded_at is set, so the status is ok
// until new failures arrive. The row is created when missing.
//
// Observations of runs still in flight may land after the reset; they
// describe the old selector only if that run resolved it before the
// re-record, which is rare and self-corrects on the next runs.
func ResetSelectorHealth(db *sql.DB, automationID, key string) error {
	now := formatHealthTime(time.Now())
	_, err := db.Exec(healthResetSQL, automationID, key, now, now)
	return err
}
