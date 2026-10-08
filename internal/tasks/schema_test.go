package tasks

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/testdb"
)

const rowTime = "2026-10-05T12:00:00Z"

// insertRow writes a minimal tasks row straight into the table.
func insertRow(db *sql.DB, profile, status, clientID string) (int64, error) {
	var cid any
	if clientID != "" {
		cid = clientID
	}
	res, err := db.Exec(`INSERT INTO tasks (profile_id, title, status, position, client_id, created_at, updated_at)
		VALUES (?, 'x', ?, 1, ?, ?, ?)`, profile, status, cid, rowTime, rowTime)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func TestMigrationCreatesTheBoardTables(t *testing.T) {
	db := testdb.Open(t).DB
	for _, table := range []string{"tasks", "task_events", "task_board_rev"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: count %d, err %v", table, n, err)
		}
	}
}

func TestATaskNeedsAnExistingProfile(t *testing.T) {
	db := testdb.Open(t).DB
	if _, err := insertRow(db, "default", "inbox", ""); err != nil {
		t.Fatalf("a task in the default profile: %v", err)
	}
	_, err := insertRow(db, "no-such-profile", "inbox", "")
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("a task in an unknown profile: err %v, want a foreign key failure", err)
	}
	for _, c := range []struct {
		name    string
		profile any
		want    string
	}{
		{"a revision row for an unknown profile", "no-such-profile", "FOREIGN KEY"},
		{"a revision row with no profile", nil, "NOT NULL"}, // SQLite lets a text primary key hold NULL unless it says NOT NULL
	} {
		_, err := db.Exec(`INSERT INTO task_board_rev (profile_id, rev) VALUES (?, 1)`, c.profile)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want a %s failure", c.name, err, c.want)
		}
	}
}

func TestDeletingAProfileDeletesItsBoard(t *testing.T) {
	db := testdb.Open(t).DB
	if _, err := db.Exec(`INSERT INTO profiles (id, name) VALUES ('p1', 'P1')`); err != nil {
		t.Fatal(err)
	}
	id, err := insertRow(db, "p1", "ready", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_events (task_id, at, actor, kind) VALUES (?, ?, 'you', 'created')`, id, rowTime); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_board_rev (profile_id, rev) VALUES ('p1', 3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM profiles WHERE id = 'p1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tasks", "task_events", "task_board_rev"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s after deleting the profile: %d rows, err %v", table, n, err)
		}
	}
}

func TestColumnChecksAndTheClientIDIndex(t *testing.T) {
	db := testdb.Open(t).DB
	if _, err := insertRow(db, "default", "bogus", ""); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("status bogus: err %v, want a CHECK failure", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (profile_id, title, source_kind, position, created_at, updated_at)
		VALUES ('default', 'x', 'carrier-pigeon', 1, ?, ?)`, rowTime, rowTime); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("source kind carrier-pigeon: err %v, want a CHECK failure", err)
	}
	if _, err := db.Exec(`INSERT INTO profiles (id, name) VALUES ('p2', 'P2')`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		profile, client string
		wantErr         string // a part of the error message; empty means no error
	}{
		{"default", "c-1", ""},
		{"default", "c-1", "UNIQUE"}, // the same key in the same profile
		{"p2", "c-1", ""},            // the same key in another profile
		{"default", "", ""},          // no key: any number of them
		{"default", "", ""},
	} {
		_, err := insertRow(db, c.profile, "inbox", c.client)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("profile %s client %q: unexpected error %v", c.profile, c.client, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("profile %s client %q: err %v, want a %s failure", c.profile, c.client, err, c.wantErr)
		}
	}
}

// A released migration cannot change a CHECK list, so every allowed value is pinned here.
func TestEveryStatusAndSourceKindIsAccepted(t *testing.T) {
	db := testdb.Open(t).DB
	for _, status := range []string{"inbox", "ready", "in_progress", "review", "done", "archived"} {
		if _, err := insertRow(db, "default", status, ""); err != nil {
			t.Errorf("status %s: %v", status, err)
		}
	}
	for _, kind := range []string{"cli", "app", "chrome", "os", "agent"} {
		if _, err := db.Exec(`INSERT INTO tasks (profile_id, title, source_kind, position, created_at, updated_at)
			VALUES ('default', 'x', ?, 1, ?, ?)`, kind, rowTime, rowTime); err != nil {
			t.Errorf("source kind %s: %v", kind, err)
		}
	}
}
