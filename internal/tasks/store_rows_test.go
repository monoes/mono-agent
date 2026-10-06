package tasks

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// What a new task is stored as, column by column.
func TestAddStoresTheExactRowAndEvent(t *testing.T) {
	s, db, c := newTestStore(t)
	// a clock in another zone: the text is still UTC
	cur := c.t.In(time.FixedZone("plus0530", 5*3600+1800))
	s.now = func() time.Time { return cur }
	task := mustAdd(t, s, "default", "go", true)
	var created, updated, claimedBy, claimUntil, clientNull string
	if err := db.QueryRow(`SELECT created_at, updated_at, claimed_by, claim_until, IFNULL(client_id, 'NULL') FROM tasks WHERE id = ?`, task.ID).Scan(&created, &updated, &claimedBy, &claimUntil, &clientNull); err != nil {
		t.Fatal(err)
	}
	if created != "2026-10-05T12:00:00Z" || updated != created || claimedBy != "" || claimUntil != "" || clientNull != "NULL" {
		t.Errorf("row: created %q updated %q claimed_by %q claim_until %q client_id %q", created, updated, claimedBy, claimUntil, clientNull)
	}
	var at, actor, kind, from, to, note string
	if err := db.QueryRow(`SELECT at, actor, kind, from_status, to_status, note FROM task_events WHERE task_id = ?`, task.ID).Scan(&at, &actor, &kind, &from, &to, &note); err != nil {
		t.Fatal(err)
	}
	if at != created || actor != "you" || kind != "created" || from != "" || to != "ready" || note != "" {
		t.Errorf("event: at %q actor %q kind %q from %q to %q note %q", at, actor, kind, from, to, note)
	}
	if !task.UpdatedAt.Equal(c.t) || !task.LastEvent.At.Equal(c.t) {
		t.Errorf("times: updated %v, last event at %v, want %v", task.UpdatedAt, task.LastEvent.At, c.t)
	}
}

func TestAddCleansTheSourceTitleAndTheAppName(t *testing.T) {
	s, _, _ := newTestStore(t)
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	hostile := "x\x1b[31m\x00\U0000202e\U000E0049\U0000FEFFy\n w"
	hidden250 := strings.Repeat("\U0000202e", 250)
	for _, c := range []struct{ name, in, want string }{
		{"escapes, NUL, bidi, tag, BOM and a newline", hostile, "x[31my w"},
		{"hidden characters do not use up the cut", hidden250 + "real", "real"},
		{"invalid UTF-8", "a\xffb", "a�b"},
	} {
		task, _, err := s.Add(bg, "default", AddInput{Title: "t", SourceKind: SourceChrome, SourceTitle: c.in, SourceApp: c.in}, chrome)
		if err != nil {
			t.Fatal(err)
		}
		if task.Source.Title != c.want || task.Source.App != c.want {
			t.Errorf("%s: source title %q, app %q, want %q for both", c.name, task.Source.Title, task.Source.App, c.want)
		}
	}
}

func TestSourceURLIsKeptAtTheLimitAndDroppedOneByteOver(t *testing.T) {
	s, _, _ := newTestStore(t)
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	head := "https://example.com/"
	for _, c := range []struct {
		name string
		url  string
		keep bool
	}{
		{"2048 bytes as typed", head + strings.Repeat("a", 2048-len(head)), true},
		{"2049 bytes as typed", head + strings.Repeat("a", 2049-len(head)), false},
		{"2048 bytes once escaped", head + strings.Repeat("é", 10) + strings.Repeat("a", 1968), true},
		{"2049 bytes once escaped", head + strings.Repeat("é", 10) + strings.Repeat("a", 1969), false},
		// The cap is on what was typed too: dropping the user-info would bring this one under 2048.
		{"2049 bytes typed, 2045 once the user-info is stripped", "https://u:p@example.com/" + strings.Repeat("a", 2049-len("https://u:p@example.com/")), false},
	} {
		task, _, err := s.Add(bg, "default", AddInput{Title: "t", SourceKind: SourceChrome, SourceURL: c.url}, chrome)
		if err != nil || (task.Source.URL != "") != c.keep {
			t.Errorf("%s: stored %d bytes, err %v, kept want %v", c.name, len(task.Source.URL), err, c.keep)
		}
	}
}

func TestProfileReturnsTheProfileOrRefuses(t *testing.T) {
	s, db, _ := newTestStore(t)
	addProfile(t, db, "p2")
	got, err := s.Profile(bg, "p2")
	if err != nil || got != (Profile{ID: "p2", Name: "Profile p2"}) {
		t.Errorf("Profile(p2) = %+v, %v", got, err)
	}
	if _, err := s.Profile(bg, "nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Profile(nope): %v, want ErrInvalid", err)
	}
	if _, err := s.Profile(bg, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("Profile(\"\"): %v, want ErrInvalid", err)
	}
}

// Helpers Tasks 4 to 6 build on.
func TestGetTxIsScopedToItsProfileAndShowsTheNewestEvent(t *testing.T) {
	s, db, c := newTestStore(t)
	other := addProfile(t, db, "p2")
	task := mustAdd(t, s, "default", "a", false)
	if _, err := db.Exec(`INSERT INTO task_events (task_id, at, actor, kind) VALUES (?, ?, 'bob', 'comment')`, task.ID, c.now().Format(timeFmt)); err != nil {
		t.Fatal(err)
	}
	got, err := s.getTx(bg, db, "default", task.ID)
	if err != nil || got.LastEvent == nil || got.LastEvent.Actor != "bob" || got.LastEvent.Kind != "comment" {
		t.Errorf("last event %+v, err %v, want bob's comment", got.LastEvent, err)
	}
	if _, err := s.getTx(bg, db, other, task.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another profile's id: %v, want ErrNotFound", err)
	}
}

func TestAClaimThatEndsExactlyNowIsStale(t *testing.T) {
	s, db, c := newTestStore(t)
	task := mustAdd(t, s, "default", "a", true)
	for _, k := range []struct {
		until time.Time
		stale bool
	}{
		{c.t.Add(-time.Second), true}, {c.t, true}, {c.t.Add(time.Second), false},
	} {
		if _, err := db.Exec(`UPDATE tasks SET status = 'in_progress', claimed_by = 'bob', claim_until = ? WHERE id = ?`, k.until.Format(timeFmt), task.ID); err != nil {
			t.Fatal(err)
		}
		got, err := s.getTx(bg, db, "default", task.ID)
		if err != nil || got.Claim == nil || got.Claim.Stale != k.stale {
			t.Errorf("lease ending %v from now: claim %+v err %v, want stale %v", k.until.Sub(c.t), got.Claim, err, k.stale)
		}
	}
}

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
