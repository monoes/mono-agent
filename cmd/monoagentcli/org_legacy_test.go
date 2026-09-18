package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Org names nest: "app" is a prefix of "app-eu". Everything that moves an
// org's files wholesale — `org legacy move` and `org rename` — walks
// legacyOrgPaths, so claiming a sibling org's artifact there silently takes
// that org's goals, threads, approvals, members and budgets with it.
func TestLegacyOrgPathsLeavesSiblingOrgFilesAlone(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{
		"app.json", "app-state.json", "app-goals.json",
		"app-eu.json", "app-eu-state.json", "app-eu-goals.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := legacyOrgPaths(dir, "app")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if strings.HasPrefix(p, "app-eu") {
			t.Fatalf("org app claimed org app-eu's file %q (all: %v)", p, got)
		}
	}
	want := map[string]bool{"app.json": true, "app": true, "app-state.json": true, "app-goals.json": true}
	if len(got) != len(want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Fatalf("unexpected path %q in %v", p, got)
		}
	}

	// The narrower org still gets everything of its own.
	eu, err := legacyOrgPaths(dir, "app-eu")
	if err != nil {
		t.Fatal(err)
	}
	if len(eu) != 3 {
		t.Fatalf("app-eu paths = %v", eu)
	}
}
