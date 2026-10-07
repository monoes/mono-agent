package storage

import (
	"strings"
	"testing"
)

// embeddedMigrations runs the duplicate check, so this fails if any two files
// in data/migrations share a numeric version prefix.
func TestEmbeddedMigrationVersionsUnique(t *testing.T) {
	all, err := embeddedMigrations()
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no embedded migrations found")
	}
}

func TestCheckDuplicateVersions(t *testing.T) {
	ok := []migration{{1, "001_a.sql"}, {2, "002_b.sql"}, {4, "004_c.sql"}}
	if err := checkDuplicateVersions(ok); err != nil {
		t.Fatalf("gaps must be allowed, got %v", err)
	}
	dup := []migration{{1, "001_a.sql"}, {2, "002_b.sql"}, {2, "002_c.sql"}}
	err := checkDuplicateVersions(dup)
	if err == nil || !strings.Contains(err.Error(), "002_b.sql") || !strings.Contains(err.Error(), "002_c.sql") {
		t.Fatalf("want duplicate error naming both files, got %v", err)
	}
}
