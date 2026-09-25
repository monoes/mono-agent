package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/vault"
)

func writeTestImageFile(t *testing.T, filename, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestVaultImages_DiscoveredAndUploaded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := newTestApp(t)

	// Add an uploaded image
	uploadPath := writeTestImageFile(t, "upload.png", "png-data")
	added, err := a.AddVaultImage(uploadPath, "Uploaded Logo")
	if err != nil {
		t.Fatalf("AddVaultImage: %v", err)
	}
	uploadedID := added["id"].(string)

	// Register a discovered image
	projectImgPath := writeTestImageFile(t, "project.png", "project-data")
	discID, _, err := vault.RegisterDiscoveredImage(context.Background(), a.db, a.getActiveProfileID(), projectImgPath, "project.png", 12)
	if err != nil {
		t.Fatalf("RegisterDiscoveredImage: %v", err)
	}

	images, err := a.GetVaultImages(10)
	if err != nil {
		t.Fatalf("GetVaultImages: %v", err)
	}
	if len(images) < 2 {
		t.Fatalf("expected at least 2 images, got %d", len(images))
	}

	// Verify DeleteVaultImage on discovered image removes row but preserves file on disk
	if err := a.DeleteVaultImage(discID); err != nil {
		t.Fatalf("DeleteVaultImage(discovered): %v", err)
	}
	if _, err := os.Stat(projectImgPath); os.IsNotExist(err) {
		t.Fatalf("DeleteVaultImage must not delete discovered image from disk")
	}

	// Verify DeleteVaultImage on uploaded image removes row
	if err := a.DeleteVaultImage(uploadedID); err != nil {
		t.Fatalf("DeleteVaultImage(upload): %v", err)
	}
}

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
