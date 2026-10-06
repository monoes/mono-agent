package tasks

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// opsEvents returns the events of a task of the default profile, oldest first, each as
// "kind|actor|from>to|note".
func opsEvents(t *testing.T, s *Store, id int64) []string {
	t.Helper()
	_, events, err := s.Get(bg, "default", id)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, fmt.Sprintf("%s|%s|%s>%s|%s", e.Kind, e.Actor, e.FromStatus, e.ToStatus, e.Note))
	}
	return out
}

// opsClaim reads the two claim columns of a task straight from the table.
func opsClaim(t *testing.T, db *sql.DB, id int64) (by, until string) {
	t.Helper()
	if err := db.QueryRow(`SELECT claimed_by, claim_until FROM tasks WHERE id = ?`, id).Scan(&by, &until); err != nil {
		t.Fatal(err)
	}
	return by, until
}

// opsHeld is an In progress card that `by` holds until `lease` from now: the operator's Move puts
// it in the column, and the two claim columns are written the way a claim writes them (the
// operator cannot claim, so a test has no verb that does).
func opsHeld(t *testing.T, s *Store, db *sql.DB, c *clock, title, by string, lease time.Duration) Task {
	t.Helper()
	task := mustAdd(t, s, "default", title, true)
	if _, err := s.Move(bg, "default", task.ID, StatusInProgress, Placement{}, human); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = ?, claim_until = ? WHERE id = ?`, by, c.t.Add(lease).Format(timeFmt), task.ID); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Get(bg, "default", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Claim == nil || got.Claim.By != by || got.Status != StatusInProgress {
		t.Fatalf("setup: %+v is not held by %s", got, by)
	}
	return got
}

// opsStray gives a task a claim without making it In progress: a row no claim could have written,
// which a reader still shows as claimed (scanTask looks at the claim columns only).
func opsStray(t *testing.T, db *sql.DB, c *clock, id int64, by string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = ?, claim_until = ? WHERE id = ?`, by, c.t.Add(time.Hour).Format(timeFmt), id); err != nil {
		t.Fatal(err)
	}
}

// opsVerb is one of the operator's verbs as a call that takes a profile, a task and an actor. With
// bad it is called with arguments that are refused whoever asks: that shows which check came first.
type opsVerb struct {
	name string
	run  func(profile string, id int64, a Actor, bad bool) error
}

func opsVerbs(s *Store) []opsVerb {
	title := "a new title"
	return []opsVerb{
		{"Edit", func(p string, id int64, a Actor, bad bool) error {
			e := Edit{Title: &title}
			if bad {
				e = Edit{} // nothing to change
			}
			_, err := s.Edit(bg, p, id, e, a)
			return err
		}},
		{"Move", func(p string, id int64, a Actor, bad bool) error {
			to, pl := StatusReady, Placement{}
			if bad {
				to, pl = StatusArchived, Placement{Top: true, Bottom: true}
			}
			_, err := s.Move(bg, p, id, to, pl, a)
			return err
		}},
		{"Approve", func(p string, id int64, a Actor, bad bool) error {
			ids := []int64{id}
			if bad {
				ids = nil
			}
			_, err := s.Approve(bg, p, ids, false, a)
			return err
		}},
		{"Archive", func(p string, id int64, a Actor, bad bool) error {
			ids := []int64{id}
			if bad {
				ids = nil
			}
			_, err := s.Archive(bg, p, ids, a)
			return err
		}},
		{"ArchiveStatus", func(p string, id int64, a Actor, bad bool) error {
			st := StatusInbox
			if bad {
				st = StatusArchived
			}
			_, err := s.ArchiveStatus(bg, p, st, a)
			return err
		}},
		{"Unarchive", func(p string, id int64, a Actor, bad bool) error {
			ids := []int64{id}
			if bad {
				ids = nil
			}
			_, err := s.Unarchive(bg, p, ids, a)
			return err
		}},
	}
}
