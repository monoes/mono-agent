package tasks

import (
	"database/sql"
	"testing"
	"time"
)

// claimsSeedEvents writes comment events straight into the table until the task holds total of them.
func claimsSeedEvents(t *testing.T, db *sql.DB, id int64, total int) {
	t.Helper()
	have := countWhere(t, db, "task_events", "task_id = ?", id)
	if have > total {
		t.Fatalf("setup: task #%d holds %d events already, more than the %d asked for", id, have, total)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := have; i < total; i++ {
		if _, err := tx.Exec(`INSERT INTO task_events (task_id, at, actor, kind) VALUES (?, ?, 'seed', 'comment')`, id, rowTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// claimsStray gives a task a claim without making it In progress, with the lease ending at until: a row
// no claim could have written, which a reader still shows as claimed.
func claimsStray(t *testing.T, db *sql.DB, id int64, by string, until time.Time) {
	t.Helper()
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = ?, claim_until = ? WHERE id = ?`, by, until.Format(timeFmt), id); err != nil {
		t.Fatal(err)
	}
}

// claimsText reads one text from the database.
func claimsText(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func claimsRev(t *testing.T, s *Store) int64 {
	t.Helper()
	rev, err := s.Rev(bg, "default")
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

// claimsClaim claims a task as an agent and fails the test when it is refused.
func claimsClaim(t *testing.T, s *Store, id int64, name string, lease time.Duration) Task {
	t.Helper()
	got, err := s.Claim(bg, "default", id, bot(name), lease)
	if err != nil {
		t.Fatalf("%s claims #%d: %v", name, id, err)
	}
	return got
}
