package tasks

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/testdb"
)

// openContended returns a store whose connection gives up on a lock after 20 ms, and a
// release function for a second connection that holds the write lock.
func openContended(t *testing.T) (*Store, func()) {
	t.Helper()
	path := testdb.Path(t)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(20)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	holder, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Close() })
	conn, err := holder.Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(bg, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	release := func() {
		select {
		case <-released:
		default:
			close(released)
			_, _ = conn.ExecContext(bg, "ROLLBACK")
			conn.Close()
		}
	}
	t.Cleanup(release)
	return NewStore(db), release
}

func shortBackoff(t *testing.T, retries int, d time.Duration) {
	t.Helper()
	r, b := busyRetries, busyBackoff
	busyRetries, busyBackoff = retries, d
	t.Cleanup(func() { busyRetries, busyBackoff = r, b })
}

// A write that meets a lock released within the retries goes through.
func TestAWriteRetriesAfterTheDatabaseWasLocked(t *testing.T) {
	shortBackoff(t, 6, 20*time.Millisecond)
	s, release := openContended(t)
	time.AfterFunc(120*time.Millisecond, release)
	if _, _, err := s.Add(bg, "default", AddInput{Title: "after the lock"}, human); err != nil {
		t.Fatalf("add during contention: %v", err)
	}
}

// A lock that is never released ends the retries and the busy error comes back.
func TestAWriteGivesUpWhenTheLockStays(t *testing.T) {
	shortBackoff(t, 2, time.Millisecond)
	s, _ := openContended(t)
	_, _, err := s.Add(bg, "default", AddInput{Title: "never"}, human)
	if err == nil || !isBusy(err) {
		t.Fatalf("want a busy error, got %v", err)
	}
}

// Only a busy database is retried.
func TestIsBusy(t *testing.T) {
	for msg, want := range map[string]bool{
		"database is locked (5) (SQLITE_BUSY)": true,
		"database table is locked":             true,
		"constraint failed":                    false,
	} {
		if got := isBusy(errors.New(msg)); got != want {
			t.Errorf("isBusy(%q) = %v", msg, got)
		}
	}
	if isBusy(nil) {
		t.Error("nil is not busy")
	}
}
