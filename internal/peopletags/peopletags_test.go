package peopletags

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, p := range [][2]string{{"p1", "prof"}, {"p2", "prof"}, {"px", "other"}} {
		if _, err := db.DB.Exec(`INSERT INTO people (id, profile_id, platform, platform_username) VALUES (?, ?, 'X', ?)`,
			p[0], p[1], p[0]); err != nil {
			t.Fatal(err)
		}
	}
	return db.DB
}

func TestAddCreatesWithColour(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	// A new tag comes back with its colour (the GUI edit this replaces
	// returned an empty one) — the default when none is given.
	tag, err := Add(ctx, db, "prof", "p1", " Hot lead ", "#ff0000")
	if err != nil || tag.Color != "#ff0000" || tag.Name != "Hot lead" || tag.ID == "" {
		t.Fatalf("Add = %+v, %v", tag, err)
	}
	other, _ := Add(ctx, db, "prof", "p1", "cold", "")
	if other.Color != DefaultColor {
		t.Fatalf("default colour: %+v", other)
	}

	// Same name, any case: the same tag; linking twice is a no-op.
	again, err := Add(ctx, db, "prof", "p2", "HOT LEAD", "")
	if err != nil || again.ID != tag.ID || again.Color != "#ff0000" {
		t.Fatalf("re-add = %+v, %v", again, err)
	}
	if _, err := Add(ctx, db, "prof", "p2", "hot lead", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := List(ctx, db, "prof", "p2"); len(got) != 1 {
		t.Fatalf("p2 tags = %+v", got)
	}

	// Re-adding with a colour recolours the tag for everyone.
	if _, err := Add(ctx, db, "prof", "p2", "hot lead", "#00ff00"); err != nil {
		t.Fatal(err)
	}
	if got, _ := List(ctx, db, "prof", "p1"); got[1].Color != "#00ff00" {
		t.Fatalf("recolour not shared: %+v", got)
	}
}

func TestAddRefusals(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	for _, c := range []struct{ person, name, color string }{
		{"p1", "  ", ""},
		{"p1", "x", "red"},
		{"p1", "x", "#12"},
	} {
		if _, err := Add(ctx, db, "prof", c.person, c.name, c.color); !errors.Is(err, ErrInvalid) {
			t.Errorf("Add(%q, %q) = %v, want ErrInvalid", c.name, c.color, err)
		}
	}
	if _, err := Add(ctx, db, "prof", "px", "x", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other profile's person: %v", err)
	}
	for i := 0; i < MaxPerPerson; i++ {
		if _, err := Add(ctx, db, "prof", "p1", fmt.Sprintf("t%d", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(ctx, db, "prof", "p1", "one too many", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over the cap: %v", err)
	}
	// Re-adding a tag the person already has is not "one more".
	if _, err := Add(ctx, db, "prof", "p1", "t0", ""); err != nil {
		t.Fatalf("re-add at the cap: %v", err)
	}
}

func TestRemoveAndSetColor(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	tag, _ := Add(ctx, db, "prof", "p1", "vip", "")
	if _, err := Add(ctx, db, "prof", "p2", "vip", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := SetColor(ctx, db, "prof", "VIP", "#abcdef"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Find(ctx, db, "prof", tag.ID); got.Color != "#abcdef" {
		t.Fatalf("colour = %+v", got)
	}
	if _, err := SetColor(ctx, db, "other", tag.ID, "#000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other profile's tag: %v", err)
	}
	if _, err := SetColor(ctx, db, "prof", tag.ID, "blue"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad colour: %v", err)
	}

	if err := Remove(ctx, db, "prof", "p1", tag.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := List(ctx, db, "prof", "p1"); len(got) != 0 {
		t.Fatalf("p1 still tagged: %+v", got)
	}
	// Unlinking keeps the tag and other people's links.
	if got, _ := List(ctx, db, "prof", "p2"); len(got) != 1 {
		t.Fatalf("p2 lost its tag: %+v", got)
	}
	if err := Remove(ctx, db, "prof", "p1", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown tag: %v", err)
	}
}
