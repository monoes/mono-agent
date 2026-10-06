package tasks

import (
	"database/sql"
	"strings"
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

// claimsExplainEveryTask replays the history of every task of the default profile, event by event, and
// fails the test wherever the history does not explain the row it left behind: a task is held by one
// agent at a time, only the holder comments, finishes or releases it, a takeover is of a claim somebody
// holds, an event goes from the status the task was in, and the status and the holder at the end are the
// row's. The operator may end any claim by moving the card away from In progress: that is a released
// event in its name, before the move's own event (which is the one that changes the status), and it
// names the holder whose claim ended. It returns how many claims the histories hold.
func claimsExplainEveryTask(t *testing.T, s *Store, db *sql.DB) int {
	t.Helper()
	rows, err := db.Query(`SELECT id, status, claimed_by FROM tasks WHERE profile_id = 'default' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id         int64
		status, by string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.status, &r.by); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	rows.Close()
	claims := 0
	for _, r := range all {
		state, holder := "", ""
		_, events, err := s.Get(bg, "default", r.id)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range events {
			operatorEnds := e.Kind == "released" && e.Actor == "you"
			if e.ToStatus != "" {
				if e.FromStatus != "" && e.FromStatus != state {
					t.Errorf("task #%d, event %d (%s): from %s while the task was in %q", r.id, i, e.Kind, e.FromStatus, state)
				}
				if !operatorEnds {
					state = e.ToStatus
				}
			}
			switch e.Kind {
			case "claimed":
				claims++
				switch {
				case e.Note == "renewed" && holder != e.Actor:
					t.Errorf("task #%d, event %d: %s renewed a claim held by %q", r.id, i, e.Actor, holder)
				case e.Note != "renewed" && holder != "":
					t.Errorf("task #%d, event %d: %s claimed a task held by %q", r.id, i, e.Actor, holder)
				case e.Note != "renewed" && e.FromStatus != "ready":
					t.Errorf("task #%d, event %d: a claim from %q", r.id, i, e.FromStatus)
				}
				holder = e.Actor
			case "reclaimed":
				if holder == "" || holder == e.Actor {
					t.Errorf("task #%d, event %d: %s took over a claim held by %q", r.id, i, e.Actor, holder)
				}
				holder = e.Actor
			case "comment":
				if e.Actor != "you" && holder != e.Actor {
					t.Errorf("task #%d, event %d: %s commented on a task held by %q", r.id, i, e.Actor, holder)
				}
			case "result", "question", "released":
				switch {
				case operatorEnds && (holder == "" || !strings.Contains(e.Note, "the claim of "+holder+" ended")):
					t.Errorf("task #%d, event %d: the operator ended a claim held by %q, saying %q", r.id, i, holder, e.Note)
				case !operatorEnds && holder != e.Actor:
					t.Errorf("task #%d, event %d: %s handed back a task held by %q", r.id, i, e.Actor, holder)
				}
				holder = ""
			}
		}
		if state != r.status || holder != r.by {
			t.Errorf("task #%d: its history says %q held by %q, the row says %q held by %q", r.id, state, holder, r.status, r.by)
		}
	}
	return claims
}
