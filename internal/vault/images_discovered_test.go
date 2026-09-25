package vault_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/vault"
)

func TestRegisterDiscoveredImage(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	imgDir := t.TempDir()
	imgPath := filepath.Join(imgDir, "logo.png")
	if err := os.WriteFile(imgPath, []byte("pngdata"), 0644); err != nil {
		t.Fatal(err)
	}

	id, created, err := vault.RegisterDiscoveredImage(ctx, db.DB, "default", imgPath, "logo.png", 7)
	if err != nil {
		t.Fatalf("RegisterDiscoveredImage: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first registration")
	}
	if id != "img-001" {
		t.Fatalf("expected id img-001, got %q", id)
	}

	// Re-registering the same path must return the existing id with created=false
	id2, created2, err := vault.RegisterDiscoveredImage(ctx, db.DB, "default", imgPath, "logo.png", 7)
	if err != nil {
		t.Fatalf("second RegisterDiscoveredImage: %v", err)
	}
	if created2 {
		t.Fatal("expected created=false on duplicate path")
	}
	if id2 != id {
		t.Fatalf("expected duplicate to return same id %q, got %q", id, id2)
	}
}

func TestReconcileDiscoveredImages(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	imgDir := t.TempDir()
	img1 := filepath.Join(imgDir, "img1.png")
	img2 := filepath.Join(imgDir, "img2.jpg")
	_ = os.WriteFile(img1, []byte("one"), 0644)
	_ = os.WriteFile(img2, []byte("two-bytes"), 0644)

	// Also add an uploaded image to verify it's never removed by discovery reconciliation
	uploadedPath := filepath.Join(t.TempDir(), "upload.png")
	_ = os.WriteFile(uploadedPath, []byte("upload"), 0644)
	vaultCtx := vault.ContextWithProfileID(ctx, "default")
	uploadedID, err := vault.Register(vaultCtx, db.DB, uploadedPath, "upload", "", "")
	if err != nil {
		t.Fatalf("vault.Register: %v", err)
	}

	found := []vault.DiscoveredFile{
		{Path: img1, Filename: "img1.png", SizeBytes: 3},
		{Path: img2, Filename: "img2.jpg", SizeBytes: 9},
	}

	added, removed, errs := vault.ReconcileDiscoveredImages(ctx, db.DB, "default", found)
	if len(errs) > 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if added != 2 || removed != 0 {
		t.Fatalf("expected added=2, removed=0; got added=%d, removed=%d", added, removed)
	}

	// Update img1 size, remove img2 from found (simulating file deleted from project)
	found2 := []vault.DiscoveredFile{
		{Path: img1, Filename: "img1.png", SizeBytes: 100},
	}
	added2, removed2, errs2 := vault.ReconcileDiscoveredImages(ctx, db.DB, "default", found2)
	if len(errs2) > 0 {
		t.Fatalf("unexpected errs: %v", errs2)
	}
	if added2 != 0 || removed2 != 1 {
		t.Fatalf("expected added=0, removed=1; got added=%d, removed=%d", added2, removed2)
	}

	// Check that img1 size was updated to 100
	var size int64
	if err := db.DB.QueryRow(`SELECT size_bytes FROM vault_images WHERE path = ?`, img1).Scan(&size); err != nil {
		t.Fatalf("query img1: %v", err)
	}
	if size != 100 {
		t.Fatalf("expected updated size 100, got %d", size)
	}

	// Check uploaded image is still present
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM vault_images WHERE id = ?`, uploadedID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("uploaded image should not have been removed")
	}
}

func TestDeleteImage_DiscoveredPreservesDiskFile(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	imgDir := t.TempDir()
	imgPath := filepath.Join(imgDir, "user_project_photo.png")
	if err := os.WriteFile(imgPath, []byte("important user work"), 0644); err != nil {
		t.Fatal(err)
	}

	id, _, err := vault.RegisterDiscoveredImage(ctx, db.DB, "default", imgPath, "user_project_photo.png", 19)
	if err != nil {
		t.Fatalf("RegisterDiscoveredImage: %v", err)
	}

	if err := vault.DeleteImage(ctx, db.DB, "default", id); err != nil {
		t.Fatalf("DeleteImage: %v", err)
	}

	// The row must be gone from vault_images
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM vault_images WHERE id = ?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expected row to be deleted")
	}

	// The file on disk must STILL exist
	if _, err := os.Stat(imgPath); os.IsNotExist(err) {
		t.Fatalf("DeleteImage must NOT delete discovered files from disk!")
	}
}
