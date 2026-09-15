package main

import (
	"path/filepath"
	"testing"
)

func TestRestrictFileWriteForOrgs_TrueForDefaultSeededProfile(t *testing.T) {
	a := newTestApp(t)
	// newTestApp's active profile defaults to "default" -- migration
	// 011_profiles.sql seeds it with an empty root_dir, so it should be
	// treated as default-managed (restricted).
	if !a.restrictFileWriteForOrgs() {
		t.Error("the seeded default profile (no root_dir) should be restricted")
	}
}

func TestRestrictFileWriteForOrgs_FalseForCustomRootDirProfile(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.db.Exec(
		"INSERT INTO profiles (id, name, root_dir) VALUES ('custom', 'Custom', ?)",
		filepath.Join(t.TempDir(), "my-existing-project"),
	); err != nil {
		t.Fatalf("seed profile failed: %v", err)
	}
	a.setActiveProfileID("custom")

	if a.restrictFileWriteForOrgs() {
		t.Error("a profile with a root_dir override should NOT be restricted")
	}
}
