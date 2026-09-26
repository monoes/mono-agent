package storage

import (
	"path/filepath"
	"testing"
)

func newReadTestDB(t *testing.T) *Database {
	t.Helper()
	db, err := NewDatabase(filepath.Join(t.TempDir(), "read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO people (id, platform_username, platform, profile_id) VALUES ('p1','a','x','default'), ('p2','b','x','default'), ('po','c','x','other')`,
	} {
		if _, err := db.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func upsertMsg(t *testing.T, db *Database, id, person, direction, profile string) {
	t.Helper()
	if err := db.UpsertPersonMessage(&PersonMessage{ID: id, PersonID: person, Source: "linkedin", ExternalID: id,
		Direction: direction, Status: "sent", Body: "hi"}, profile); err != nil {
		t.Fatal(err)
	}
}

func TestMessageReadState(t *testing.T) {
	db := newReadTestDB(t)
	upsertMsg(t, db, "m1", "p1", "inbound", "default")
	upsertMsg(t, db, "m2", "p1", "inbound", "default")
	upsertMsg(t, db, "m3", "p2", "inbound", "default")
	upsertMsg(t, db, "m4", "p1", "outbound", "default")
	upsertMsg(t, db, "mo", "po", "inbound", "other")

	if n, err := db.CountUnreadPersonMessages("default"); err != nil || n != 3 {
		t.Fatalf("unread = %d, %v (new inbound starts unread; outbound never counts)", n, err)
	}
	unread, err := db.ListAllPersonMessagesFiltered("default", "", true, 50, 0)
	if err != nil || len(unread) != 3 || unread[0].ReadAt != nil {
		t.Fatalf("unread list = %d, %v", len(unread), err)
	}
	if n, err := db.MarkPersonMessagesRead("default", "p1", nil); err != nil || n != 2 {
		t.Fatalf("mark person read = %d, %v", n, err)
	}
	if n, _ := db.MarkPersonMessagesRead("default", "", []string{"m3", "mo"}); n != 1 {
		t.Fatalf("marking another profile's message must not count: %d", n)
	}
	if n, _ := db.CountUnreadPersonMessages("default"); n != 0 {
		t.Fatalf("unread after marking = %d", n)
	}
	m, err := db.GetPersonMessage("m1")
	if err != nil || m.ReadAt == nil {
		t.Fatalf("m1 read_at = %v, %v", m, err)
	}
	if err := db.MarkPersonMessageUnread("default", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkPersonMessageUnread("default", "m4"); err == nil {
		t.Fatal("an outbound message can't be unread")
	}
	if n, _ := db.CountUnreadPersonMessages("other"); n != 1 {
		t.Fatalf("other profile untouched: %d", n)
	}
	// Re-syncing a message keeps its read state.
	if _, err := db.MarkPersonMessagesRead("default", "", []string{"m1"}); err != nil {
		t.Fatal(err)
	}
	upsertMsg(t, db, "m1", "p1", "inbound", "default")
	if m, _ := db.GetPersonMessage("m1"); m.ReadAt == nil {
		t.Fatal("re-sync reset read_at")
	}
	if _, err := db.MarkPersonMessagesRead("default", "", nil); err == nil {
		t.Fatal("marking nothing must be refused")
	}
}

