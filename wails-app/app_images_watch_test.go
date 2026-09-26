package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestartImageWatcher_DiscoversProjectImages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := newTestApp(t)

	// Point profile root_dir to a temporary directory
	projDir := t.TempDir()
	if _, err := a.db.Exec(`UPDATE profiles SET root_dir = ? WHERE id = ?`, projDir, a.getActiveProfileID()); err != nil {
		t.Fatalf("set root_dir: %v", err)
	}

	// Create an image in the project directory
	testImg := filepath.Join(projDir, "banner.webp")
	if err := os.WriteFile(testImg, []byte("webp-binary-data"), 0644); err != nil {
		t.Fatal(err)
	}

	a.restartImageWatcher()
	defer func() {
		a.imgWatchMu.Lock()
		if a.imgWatcher != nil {
			a.imgWatcher.Stop()
			a.imgWatcher = nil
		}
		a.imgWatchMu.Unlock()
	}()

	// Wait up to 2 seconds for initial discovery scan to register the image
	var found bool
	for start := time.Now(); time.Since(start) < 2*time.Second; {
		var count int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM vault_images WHERE path = ?`, testImg).Scan(&count); err == nil && count > 0 {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Fatalf("expected image watcher to discover %s", testImg)
	}
}
