package tasks

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A replay is an Add with a client id that the profile already holds. Spec 5.3: it returns the task
// that exists, whatever has become of it since, and writes nothing.

func TestClientIDMakesAddIdempotent(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	in := AddInput{Title: "once", ClientID: "c-1", SourceKind: SourceChrome}
	first, created, err := s.Add(bg, "default", in, chrome)
	if err != nil || !created {
		t.Fatalf("first: created %v, err %v", created, err)
	}
	second, created, err := s.Add(bg, "default", in, chrome)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second: %+v created %v err %v, want the first task back", second, created, err)
	}
	if n := countWhere(t, db, "tasks", "profile_id = 'default'"); n != 1 {
		t.Errorf("%d tasks in the profile", n)
	}
	third, created, err := s.Add(bg, other, in, chrome)
	if err != nil || !created || third.ID == first.ID {
		t.Errorf("the same key in another profile: %+v created %v err %v", third, created, err)
	}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "x", ClientID: "bad id!", SourceKind: SourceChrome}, chrome); !errors.Is(err, ErrInvalid) {
		t.Errorf("a malformed client id: %v", err)
	}
}

// Spec 5.3: a replay returns the task "whatever has become of it since", and a
// capture surface retries until it hears yes, so no limit may turn it away.
func TestClientIDReplayReturnsTheTaskWhateverBecameOfIt(t *testing.T) {
	s, db, _ := newTestStore(t)
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	in := AddInput{Title: "once", ClientID: "c-1", SourceKind: SourceChrome}
	first, _, err := s.Add(bg, "default", in, chrome)
	if err != nil {
		t.Fatal(err)
	}
	replay := func(when string, want Status) {
		t.Helper()
		got, created, err := s.Add(bg, "default", in, chrome)
		if err != nil || created || got.ID != first.ID || got.Status != want {
			t.Errorf("%s: task %d as %q, created %v, err %v, want task %d back as %q", when, got.ID, got.Status, created, err, first.ID, want)
		}
	}
	seedTasks(t, db, "default", MaxOpenTasks-1) // the board is full now
	replay("on a full board", StatusInbox)
	if _, err := db.Exec(`UPDATE tasks SET status = 'archived' WHERE id = ?`, first.ID); err != nil {
		t.Fatal(err)
	}
	replay("after it was archived", StatusArchived)
	if n := countWhere(t, db, "tasks", "client_id = 'c-1'"); n != 1 {
		t.Errorf("%d tasks hold the client id", n)
	}
}

func dumpBoard(t *testing.T, db *sql.DB) string {
	t.Helper()
	var sb strings.Builder
	for _, q := range []string{
		`SELECT * FROM tasks ORDER BY id`,
		`SELECT * FROM task_events ORDER BY id`,
		`SELECT * FROM task_board_rev ORDER BY profile_id`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			sb.WriteString(fmt.Sprintln(vals...))
		}
		rows.Close()
	}
	return sb.String()
}

// Spec 5.3: a replay "writes nothing": no row, no event, no revision, not even updated_at.
func TestAReplayWritesNothingAtAll(t *testing.T) {
	s, db, c := newTestStore(t)
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	first, _, err := s.Add(bg, "default", AddInput{Title: "once", ClientID: "c-1", SourceKind: SourceChrome}, chrome)
	if err != nil {
		t.Fatal(err)
	}
	before := dumpBoard(t, db)
	c.advance(5 * time.Minute)
	for _, in := range []AddInput{
		{Title: "once", ClientID: "c-1", SourceKind: SourceChrome},
		{Title: "other words", ClientID: "c-1", SourceKind: SourceChrome, SourceURL: "https://x.example/"},
	} {
		got, created, err := s.Add(bg, "default", in, chrome)
		if err != nil || created || got.ID != first.ID || got.Title != "once" {
			t.Errorf("replay of %+v: %+v created %v err %v", in, got, created, err)
		}
	}
	if after := dumpBoard(t, db); after != before {
		t.Errorf("a replay changed the database:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// An agent that retries its twentieth task (the reply was lost) gets the task, not a refusal.
func TestAnAgentsRetryAtTheHourlyLimitGetsItsTaskBack(t *testing.T) {
	s, _, _ := newTestStore(t)
	for i := 0; i < 19; i++ {
		if _, _, err := s.Add(bg, "default", AddInput{Title: "a"}, bot("b")); err != nil {
			t.Fatal(err)
		}
	}
	in := AddInput{Title: "twentieth", ClientID: "agent-20"}
	first, created, err := s.Add(bg, "default", in, bot("b"))
	if err != nil || !created {
		t.Fatalf("twentieth: created %v err %v", created, err)
	}
	again, created, err := s.Add(bg, "default", in, bot("b"))
	if err != nil || created || again.ID != first.ID {
		t.Errorf("retry at the limit: %+v created %v err %v, want the first task back", again, created, err)
	}
}
