package profiledir

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func listTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE profiles (id TEXT PRIMARY KEY, name TEXT, created_at TEXT, root_dir TEXT)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return db
}

func TestList_MarksTheActiveProfile(t *testing.T) {
	db := listTestDB(t)
	db.Exec(`INSERT INTO profiles (id, name, created_at) VALUES ('p1', 'Personal', '2026-01-01')`)
	db.Exec(`INSERT INTO profiles (id, name, created_at) VALUES ('p2', 'Work', '2026-02-01')`)
	db.Exec(`INSERT INTO settings (key, value) VALUES ('active_profile_id', 'p2')`)

	got, err := List(context.Background(), db)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List = %+v, want 2 profiles", got)
	}
	if got[0].ID != "p1" || got[0].Name != "Personal" || got[0].Default {
		t.Errorf("first profile = %+v", got[0])
	}
	if got[1].ID != "p2" || !got[1].Default {
		t.Errorf("second profile = %+v", got[1])
	}
	if id := Default(context.Background(), db); id != "p2" {
		t.Errorf("Default = %q, want p2", id)
	}
}

// With nothing marked active — and with no settings row at all — a chooser
// still needs one profile to fall back to, so the oldest is it.
func TestList_FallsBackToTheOldest(t *testing.T) {
	db := listTestDB(t)
	db.Exec(`INSERT INTO profiles (id, name, created_at) VALUES ('p2', 'Work', '2026-02-01')`)
	db.Exec(`INSERT INTO profiles (id, name, created_at) VALUES ('p1', 'Personal', '2026-01-01')`)

	got, err := List(context.Background(), db)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got[0].ID != "p1" || !got[0].Default {
		t.Errorf("List = %+v, want p1 first and default", got)
	}
}

func TestList_EmptyIsNotAnError(t *testing.T) {
	got, err := List(context.Background(), listTestDB(t))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %+v, want empty", got)
	}
	if got == nil {
		t.Error("List returned nil, want an empty slice (it is JSON)")
	}
}

// A hand-edited database could hold an id that is not usable as a path
// component. It must never be offered as a capture destination.
func TestList_SkipsUnusableIDs(t *testing.T) {
	db := listTestDB(t)
	db.Exec(`INSERT INTO profiles (id, name, created_at) VALUES ('../evil', 'Evil', '2026-01-01')`)
	db.Exec(`INSERT INTO profiles (id, name, created_at) VALUES ('p1', 'Personal', '2026-02-01')`)

	got, err := List(context.Background(), db)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("List = %+v, want only p1", got)
	}
	if !got[0].Default {
		t.Error("the surviving profile should be the fallback default")
	}
}

func TestList_NilDBIsAnError(t *testing.T) {
	if _, err := List(context.Background(), nil); err == nil {
		t.Error("List(nil) should fail")
	}
}

// A missing settings table is a database from before profile switching; the
// profiles still list.
func TestList_ToleratesMissingSettingsTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.Exec(`CREATE TABLE profiles (id TEXT PRIMARY KEY, name TEXT, created_at TEXT)`)
	db.Exec(`INSERT INTO profiles (id, name, created_at) VALUES ('p1', 'Personal', '2026-01-01')`)

	got, err := List(context.Background(), db)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || !got[0].Default {
		t.Errorf("List = %+v", got)
	}
}
