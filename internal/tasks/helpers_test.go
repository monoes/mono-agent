package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/testdb"
)

var (
	bg    = context.Background()
	human = Actor{Kind: Human}
)

func bot(name string) Actor { return Actor{Kind: Agent, Name: name} }

// clock is a time the test controls, for leases and the hourly limit.
type clock struct{ t time.Time }

func newClock() *clock                   { return &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)} }
func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestStore is a store over a migrated database of its own (the profile
// "default" exists) with a clock the test controls.
func newTestStore(t *testing.T) (*Store, *sql.DB, *clock) {
	t.Helper()
	db := testdb.Open(t).DB
	s := NewStore(db)
	c := newClock()
	s.now = c.now
	return s, db, c
}

// addProfile inserts a profile and returns its id.
func addProfile(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO profiles (id, name) VALUES (?, ?)`, id, "Profile "+id); err != nil {
		t.Fatal(err)
	}
	return id
}

// mustAdd adds an operator task: to Inbox, or to Ready with ready.
func mustAdd(t *testing.T, s *Store, profile, title string, ready bool) Task {
	t.Helper()
	task, _, err := s.Add(bg, profile, AddInput{Title: title, Ready: ready}, human)
	if err != nil {
		t.Fatalf("add %q: %v", title, err)
	}
	return task
}

// countWhere counts the rows of a table that match a where clause.
func countWhere(t *testing.T, db *sql.DB, table, where string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// seedTasks inserts n Inbox rows straight into the table, fast.
func seedTasks(t *testing.T, db *sql.DB, profile string, n int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := tx.Exec(`INSERT INTO tasks (profile_id, title, position, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			profile, fmt.Sprintf("seed %d", i), i, rowTime, rowTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
