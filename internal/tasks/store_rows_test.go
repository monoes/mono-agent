package tasks

import (
	"errors"
	"strings"
	"testing"
)

// A stored time that does not read is an error, not a task from the year 1 (and, for a claim, a
// lease long run out): a writer that gets the format wrong must fail a test, not shift the board.
func TestAStoredTimeThatIsNotATimeIsAnError(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, c := range []struct{ name, spoil string }{
		{"created_at", `UPDATE tasks SET created_at = 'yesterday' WHERE id = ?`},
		{"updated_at", `UPDATE tasks SET updated_at = '' WHERE id = ?`},
		{"claim_until of a claimed task", `UPDATE tasks SET status = 'in_progress', claimed_by = 'bob', claim_until = '2026-10-05 12:00:00' WHERE id = ?`},
		{"the time of the last event", `UPDATE task_events SET at = '5 October' WHERE task_id = ?`},
	} {
		task := mustAdd(t, s, "default", "a", true)
		if _, err := db.Exec(c.spoil, task.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.getTx(bg, db, "default", task.ID); err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "not a time") {
			t.Errorf("%s: err %v, want an error saying the stored value is not a time", c.name, err)
		}
	}
}
