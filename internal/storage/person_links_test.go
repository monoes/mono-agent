package storage

import (
	"errors"
	"path/filepath"
	"testing"
)

func newLinksTestDB(t *testing.T) *Database {
	t.Helper()
	db, err := NewDatabase(filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO people (id, platform_username, platform, profile_id) VALUES ('pa', 'alice', 'x', 'default')`,
		`INSERT INTO people (id, platform_username, platform, profile_id) VALUES ('pb', 'alice', 'instagram', 'default')`,
		`INSERT INTO people (id, platform_username, platform, profile_id) VALUES ('pc', 'carol', 'linkedin', 'default')`,
	} {
		if _, err := db.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestPersonLinksMigrationApplies(t *testing.T) {
	db := newLinksTestDB(t)
	var n int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM person_links`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("person_links: n=%d err=%v", n, err)
	}
	// The CHECK constraints reject unknown values.
	if _, err := db.DB.Exec(`INSERT INTO person_links (id, person_a, person_b, relation, status) VALUES ('x','pa','pb','maybe','suggested')`); err == nil {
		t.Fatal("relation CHECK not enforced")
	}
	if _, err := db.DB.Exec(`INSERT INTO person_links (id, person_a, person_b, relation, status) VALUES ('x','pa','pb','same','pending')`); err == nil {
		t.Fatal("status CHECK not enforced")
	}
}

func TestInsertPersonLinkOrdersAndDedupes(t *testing.T) {
	db := newLinksTestDB(t)
	l := &PersonLink{ProfileID: "default", PersonA: "pb", PersonB: "pa", Relation: PersonLinkSame,
		Status: PersonLinkSuggested, Confidence: 0.93, Source: "jev:jev-test", Model: "jev-test"}
	ok, err := db.InsertPersonLink(l)
	if err != nil || !ok {
		t.Fatalf("insert = %v, %v", ok, err)
	}
	if l.PersonA != "pa" || l.PersonB != "pb" || l.ID == "" {
		t.Fatalf("not normalised: %+v", l)
	}
	// Same pair in the other order is a no-op, and never overwrites.
	ok, err = db.InsertPersonLink(&PersonLink{ProfileID: "default", PersonA: "pa", PersonB: "pb",
		Relation: PersonLinkNotSame, Status: PersonLinkDismissed})
	if err != nil || ok {
		t.Fatalf("duplicate insert = %v, %v", ok, err)
	}
	if _, err := db.InsertPersonLink(&PersonLink{ProfileID: "default", PersonA: "pa", PersonB: "pa",
		Relation: PersonLinkSame, Status: PersonLinkSuggested}); err == nil {
		t.Fatal("self link accepted")
	}
	if _, err := db.InsertPersonLink(&PersonLink{ProfileID: "default", PersonA: "pa", PersonB: "pc",
		Relation: "maybe", Status: PersonLinkSuggested}); err == nil {
		t.Fatal("bad relation accepted")
	}

	links, err := db.ListPersonLinks("default", "")
	if err != nil || len(links) != 1 {
		t.Fatalf("list = %v, %v", links, err)
	}
	got := links[0]
	if got.Relation != PersonLinkSame || got.Status != PersonLinkSuggested || got.Confidence != 0.93 ||
		got.Source != "jev:jev-test" || got.Model != "jev-test" || got.CreatedAt == "" {
		t.Fatalf("row = %+v", got)
	}
	if other, _ := db.ListPersonLinks("work", ""); len(other) != 0 {
		t.Fatalf("other profile sees %v", other)
	}
}

func TestSetPersonLinkStatus(t *testing.T) {
	db := newLinksTestDB(t)
	l := &PersonLink{ProfileID: "default", PersonA: "pa", PersonB: "pb", Relation: PersonLinkSame, Status: PersonLinkSuggested}
	if _, err := db.InsertPersonLink(l); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPersonLinkStatus("default", l.ID, PersonLinkConfirmed); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ListPersonLinks("default", PersonLinkConfirmed); len(got) != 1 || got[0].Relation != PersonLinkSame {
		t.Fatalf("confirmed = %+v", got)
	}
	if got, _ := db.ListPersonLinks("default", PersonLinkSuggested); len(got) != 0 {
		t.Fatalf("still suggested: %+v", got)
	}
	if err := db.SetPersonLinkStatus("default", l.ID, PersonLinkDismissed); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ListPersonLinks("default", PersonLinkDismissed); len(got) != 1 || got[0].Relation != PersonLinkNotSame {
		t.Fatalf("dismissed = %+v", got)
	}
	if err := db.SetPersonLinkStatus("work", l.ID, PersonLinkConfirmed); !errors.Is(err, ErrPersonLinkNotFound) {
		t.Fatalf("other profile: %v", err)
	}
	if err := db.SetPersonLinkStatus("default", "nope", PersonLinkConfirmed); !errors.Is(err, ErrPersonLinkNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if err := db.SetPersonLinkStatus("default", l.ID, "maybe"); err == nil || errors.Is(err, ErrPersonLinkNotFound) {
		t.Fatalf("bad status: %v", err)
	}
}

func TestPersonLinksFor(t *testing.T) {
	db := newLinksTestDB(t)
	for _, pair := range [][2]string{{"pa", "pb"}, {"pb", "pc"}} {
		if _, err := db.InsertPersonLink(&PersonLink{ProfileID: "default", PersonA: pair[0], PersonB: pair[1],
			Relation: PersonLinkSame, Status: PersonLinkSuggested}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := db.PersonLinksFor("default", "pb"); err != nil || len(got) != 2 {
		t.Fatalf("pb = %v, %v", got, err)
	}
	if got, _ := db.PersonLinksFor("default", "pa"); len(got) != 1 || got[0].Other("pa") != "pb" {
		t.Fatalf("pa = %+v", got)
	}
	// Deleting a person removes their links.
	if _, err := db.DB.Exec(`DELETE FROM people WHERE id = 'pc'`); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.PersonLinksFor("default", "pb"); len(got) != 1 {
		t.Fatalf("after delete = %+v", got)
	}
}
