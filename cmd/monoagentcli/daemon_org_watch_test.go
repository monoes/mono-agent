package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/storage"
)

// staleProvider points the writer's provider somewhere old, the way a moved
// or hand-edited org file looks before reconcile regenerates it.
func staleProvider(t *testing.T, root string) {
	t.Helper()
	d, err := orgdesign.LoadPath(filepath.Join(orgdesign.OrgsDir(root), "growth.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := d.FindRole("writer")
	r.ToolProviders[0].Command = "/old/place/monoagentcli"
	if _, err := orgdesign.Save(root, d); err != nil {
		t.Fatal(err)
	}
}

func providerCommand(t *testing.T, root string) string {
	t.Helper()
	d, err := orgdesign.LoadPath(filepath.Join(orgdesign.OrgsDir(root), "growth.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := d.FindRole("writer")
	return r.ToolProviders[0].Command
}

func watchedRoot(s *orgServices, profileID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ow := s.watchers[profileID]; ow != nil {
		return ow.root
	}
	return ""
}

// C-24 gap: the daemon's org-file watchers follow a profile folder that
// moves, and profiles created or removed, while the daemon runs.
func TestDaemonWatchersFollowProfiles(t *testing.T) {
	f := newOrgCLIFixture(t)
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
	f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "publish_post")

	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &orgServices{db: db, logf: t.Logf, watchInterval: 20 * time.Millisecond}
	s.syncWatchers(ctx, false)
	defer s.stopWatchers()
	if got := watchedRoot(s, "default"); got != filepath.Clean(f.root) {
		t.Fatalf("default watched at %q, want %q", got, f.root)
	}

	// Move the folder, as App.MoveProfileFolder does, and edit it on the
	// way so the new place starts out stale.
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(f.root, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`INSERT INTO profiles (id, name, created_at, root_dir) VALUES ('default', 'Default', '2026-01-01', ?)
		ON CONFLICT(id) DO UPDATE SET root_dir = excluded.root_dir`, moved); err != nil {
		t.Fatal(err)
	}
	staleProvider(t, moved)
	s.syncWatchers(ctx, true)
	if got := watchedRoot(s, "default"); got != moved {
		t.Fatalf("after the move, default watched at %q, want %q", got, moved)
	}
	if got := providerCommand(t, moved); got != selfExecutable() {
		t.Fatalf("the moved folder was not reconciled: provider command %q", got)
	}

	// An edit at the new place is now seen by its watcher.
	staleProvider(t, moved)
	deadline := time.Now().Add(5 * time.Second)
	for providerCommand(t, moved) != selfExecutable() {
		if time.Now().After(deadline) {
			t.Fatal("an edit at the moved folder was never reconciled")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A profile created while the daemon runs is watched; removed, it is not.
	extra := t.TempDir()
	if _, err := db.DB.Exec(`INSERT INTO profiles (id, name, created_at, root_dir) VALUES ('p-new', 'New', '2026-01-01', ?)`, extra); err != nil {
		t.Fatal(err)
	}
	s.syncWatchers(ctx, true)
	if got := watchedRoot(s, "p-new"); got != filepath.Clean(extra) {
		t.Fatalf("new profile watched at %q, want %q", got, extra)
	}
	if _, err := db.DB.Exec(`DELETE FROM profiles WHERE id = 'p-new'`); err != nil {
		t.Fatal(err)
	}
	s.syncWatchers(ctx, true)
	if got := watchedRoot(s, "p-new"); got != "" {
		t.Fatalf("removed profile still watched at %q", got)
	}
}
