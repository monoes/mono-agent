package vault_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/imagescan"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/vault"
)

// writeProfileImage writes an image file at rel inside the default
// profile's folder and returns its path.
func writeProfileImage(t *testing.T, db *sql.DB, rel, content string) string {
	t.Helper()
	path := filepath.Join(profiledir.Root(db, "default"), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// syncProfileFolder runs what `image sync` runs for the default profile.
func syncProfileFolder(t *testing.T, db *sql.DB) vault.ReconcileResult {
	t.Helper()
	files, err := imagescan.Scan(profiledir.Root(db, "default"))
	if err != nil {
		t.Fatal(err)
	}
	found := make([]vault.DiscoveredFile, len(files))
	for i, f := range files {
		found[i] = vault.DiscoveredFile{Path: f.Path, Filename: f.Filename, SizeBytes: f.SizeBytes}
	}
	r := vault.SyncDiscoveredImages(context.Background(), db, "default", found)
	if len(r.Errs) != 0 {
		t.Fatalf("sync: %v", r.Errs)
	}
	return r
}

func countImages(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vault_images WHERE profile_id = 'default'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// An image the chat saves into the profile folder is recorded where it is,
// with its own source, and the next sync does not list it a second time.
func TestRegister_ProfileFolderFileIsRecordedInPlace(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithProfileID(context.Background(), "default")
	path := writeProfileImage(t, db.DB, filepath.Join("images", "logo.png"), "png-bytes")

	id, err := vault.Register(ctx, db.DB, path, "chat", "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	im, err := vault.GetImage(ctx, db.DB, "default", id)
	if err != nil {
		t.Fatal(err)
	}
	if im.Path != path || im.Source != "chat" || im.Filename != "logo.png" || im.SizeBytes != 9 {
		t.Fatalf("image = %+v, want chat row for %s", im, path)
	}
	if entries, _ := os.ReadDir(vault.VaultDir(db.DB, "default")); len(entries) != 0 {
		t.Errorf("file was copied into the vault: %v", entries)
	}

	if r := syncProfileFolder(t, db.DB); r.Added != 0 || r.Removed != 0 {
		t.Fatalf("sync after chat save = %+v, want nothing added", r)
	}
	if n := countImages(t, db.DB); n != 1 {
		t.Fatalf("%d images, want 1", n)
	}

	// Registering the same file again (e.g. `image add` on it) keeps the row.
	again, err := vault.Register(ctx, db.DB, path, "upload", "", "")
	if err != nil || again != id {
		t.Fatalf("second Register = %q, %v; want %q", again, err, id)
	}
	if n := countImages(t, db.DB); n != 1 {
		t.Fatalf("%d images after re-register, want 1", n)
	}
}

// A file outside the profile folder, or in a part of it sync never scans,
// is still copied into the vault.
func TestRegister_FileOutsideScannedFolderIsCopied(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithProfileID(context.Background(), "default")
	outside := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	hidden := writeProfileImage(t, db.DB, filepath.Join(".cache", "pic.png"), "y")
	for _, src := range []string{outside, hidden} {
		id, err := vault.Register(ctx, db.DB, src, "upload", "", "")
		if err != nil {
			t.Fatalf("Register(%s): %v", src, err)
		}
		im, _ := vault.GetImage(ctx, db.DB, "default", id)
		if filepath.Dir(im.Path) != vault.VaultDir(db.DB, "default") {
			t.Errorf("Register(%s) stored %s, want a vault copy", src, im.Path)
		}
	}
}

// Sync counts a row of any source on a scanned path as tracking it, and
// refreshes its size.
func TestSyncDiscoveredImages_AnySourceTracksItsPath(t *testing.T) {
	db := newTestDB(t)
	path := writeProfileImage(t, db.DB, "banner.png", "0123456789")
	if _, err := db.DB.Exec(`INSERT INTO vault_images (id, seq, path, filename, size_bytes, source, profile_id)
		VALUES ('img-001', 1, ?, 'banner.png', 3, 'workflow', 'default')`, path); err != nil {
		t.Fatal(err)
	}
	if r := syncProfileFolder(t, db.DB); r.Added != 0 || r.Updated != 1 {
		t.Fatalf("sync = %+v, want the workflow row's size refreshed and nothing added", r)
	}
	var size int64
	_ = db.DB.QueryRow(`SELECT size_bytes FROM vault_images WHERE id = 'img-001'`).Scan(&size)
	if size != 10 {
		t.Errorf("size = %d, want 10", size)
	}
	// Its file vanishing never removes a non-discovered row.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if r := syncProfileFolder(t, db.DB); r.Removed != 0 || countImages(t, db.DB) != 1 {
		t.Fatalf("sync after removal = %+v; the workflow row must stay", r)
	}
}

// Deleting a discovered image keeps the file and keeps it out of the vault
// on later syncs, until it is registered again or unignored.
func TestDeleteImage_DiscoveredStaysDeletedAcrossSync(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithProfileID(context.Background(), "default")
	path := writeProfileImage(t, db.DB, filepath.Join("a", "logo.png"), "logo")
	if r := syncProfileFolder(t, db.DB); r.Added != 1 {
		t.Fatalf("first sync = %+v", r)
	}
	images, _ := vault.ListImages(ctx, db.DB, "default", 10)
	if err := vault.DeleteImage(ctx, db.DB, "default", images[0].ID); err != nil {
		t.Fatalf("DeleteImage: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("discovered file removed from disk: %v", err)
	}
	if r := syncProfileFolder(t, db.DB); r.Added != 0 || countImages(t, db.DB) != 0 {
		t.Fatalf("sync after delete = %+v; the deleted image came back", r)
	}

	if ok, err := vault.UnignoreImagePath(ctx, db.DB, "default", path); err != nil || !ok {
		t.Fatalf("UnignoreImagePath = %v, %v", ok, err)
	}
	if r := syncProfileFolder(t, db.DB); r.Added != 1 {
		t.Fatalf("sync after unignore = %+v, want it back", r)
	}
	if ok, _ := vault.UnignoreImagePath(ctx, db.DB, "default", path); ok {
		t.Error("second unignore reported an entry")
	}
}

// Deleting an in-place chat image keeps the user's file, like a discovered
// one; `image add` on the path brings it back.
func TestDeleteImage_InPlaceKeepsFileAndReAddClearsIgnore(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithProfileID(context.Background(), "default")
	path := writeProfileImage(t, db.DB, filepath.Join("images", "chart.png"), "chart")
	id, err := vault.Register(ctx, db.DB, path, "chat", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.DeleteImage(ctx, db.DB, "default", id); err != nil {
		t.Fatalf("DeleteImage: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("in-place file removed from disk: %v", err)
	}
	if r := syncProfileFolder(t, db.DB); r.Added != 0 || countImages(t, db.DB) != 0 {
		t.Fatalf("sync after delete = %+v; the deleted image came back", r)
	}

	newID, err := vault.Register(ctx, db.DB, path, "upload", "", "")
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if im, err := vault.GetImage(ctx, db.DB, "default", newID); err != nil || im.Path != path || im.Source != "upload" {
		t.Fatalf("re-added image = %+v, %v", im, err)
	}
	if err := vault.DeleteImage(ctx, db.DB, "default", newID); err != nil {
		t.Fatal(err)
	}
	// The re-add cleared the ignore entry; the delete wrote it again.
	if r := syncProfileFolder(t, db.DB); r.Added != 0 {
		t.Fatalf("sync after second delete = %+v", r)
	}
}

// A copied (uploaded) image's vault file still goes with its row, and no
// ignore entry is written for it.
func TestDeleteImage_UploadRemovesVaultCopy(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithProfileID(context.Background(), "default")
	src := filepath.Join(t.TempDir(), "up.png")
	if err := os.WriteFile(src, []byte("u"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := vault.Register(ctx, db.DB, src, "upload", "", "")
	if err != nil {
		t.Fatal(err)
	}
	im, _ := vault.GetImage(ctx, db.DB, "default", id)
	if err := vault.DeleteImage(ctx, db.DB, "default", id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(im.Path); !os.IsNotExist(err) {
		t.Errorf("vault copy %s still exists (%v)", im.Path, err)
	}
	var n int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM vault_image_ignored`).Scan(&n)
	if n != 0 {
		t.Errorf("%d ignore entries for an uploaded image", n)
	}
}

// Two discovered files with the same name get distinct URLs, each serving
// its own file.
func TestImageURLIsByID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	a := writeProfileImage(t, db.DB, filepath.Join("a", "logo.png"), "a")
	b := writeProfileImage(t, db.DB, filepath.Join("b", "logo.png"), "b")
	syncProfileFolder(t, db.DB)
	images, err := vault.ListImages(ctx, db.DB, "default", 10)
	if err != nil || len(images) != 2 {
		t.Fatalf("ListImages = %v, %v", images, err)
	}
	if images[0].URL == images[1].URL {
		t.Fatalf("both images have URL %s", images[0].URL)
	}
	want := map[string]bool{a: true, b: true}
	for _, im := range images {
		if im.URL != "/vault-image/"+im.ID {
			t.Errorf("URL = %s, want /vault-image/%s", im.URL, im.ID)
		}
		path, ok, err := vault.ImagePathInProfile(ctx, db.DB, "default", filepath.Base(im.URL))
		if err != nil || !ok || path != im.Path || !want[path] {
			t.Errorf("URL %s serves %q (%v, %v), want %s", im.URL, path, ok, err, im.Path)
		}
	}
}
