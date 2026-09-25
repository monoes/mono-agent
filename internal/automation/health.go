package automation

import (
	"database/sql"
	"time"
)

// Selector health (spec §8.7) is split across:
//
//	health.go          reading rows (doctor, Health tab)
//	health_status.go   the ok / decaying / broken rule
//	health_observer.go the batched, non-blocking writer (HealthObserver)
//	health_promote.go  promoting healed candidates (package or overlay)

// LoadSelectorHealth returns the health rows of automationID ("" = all),
// ordered by automation and key, each with its Status set.
func LoadSelectorHealth(db *sql.DB, automationID string) ([]SelectorHealth, error) {
	q := `SELECT automation_id, selector_key, ok_count, fail_count, healed_count,
	             last_ok_at, last_fail_at, last_candidate_index, recent
	      FROM automation_selector_health`
	var args []any
	if automationID != "" {
		q += ` WHERE automation_id = ?`
		args = append(args, automationID)
	}
	q += ` ORDER BY automation_id, selector_key`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SelectorHealth
	for rows.Next() {
		var h SelectorHealth
		var lastOK, lastFail sql.NullString
		if err := rows.Scan(&h.AutomationID, &h.Key, &h.OK, &h.Fail, &h.Healed,
			&lastOK, &lastFail, &h.LastCandidateIndex, &h.Recent); err != nil {
			return nil, err
		}
		h.LastOK, h.LastFail = parseHealthTime(lastOK), parseHealthTime(lastFail)
		h.Status = SelectorStatus(h)
		out = append(out, h)
	}
	return out, rows.Err()
}

func parseHealthTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s.String)
	if err != nil {
		return nil
	}
	return &t
}
