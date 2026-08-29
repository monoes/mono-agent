package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMoveFiles_MovesAndUpdatesPaths(t *testing.T) {
	db := newVaultTestDB(t)
	fromDir := t.TempDir()
	toDir := filepath.Join(t.TempDir(), "dest") // doesn't exist yet — MoveFiles must create it

	srcPath := filepath.Join(fromDir, "img-001.png")
	content := []byte("fake image bytes")
	if err := os.WriteFile(srcPath, content, 0600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vault_images (id, path, filename, profile_id) VALUES ('img-001', ?, 'img-001.png', 'p1')`, srcPath); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	moved, errs := MoveFiles(context.Background(), db, "p1", fromDir, toDir)
	if len(errs) != 0 {
		t.Fatalf("MoveFiles errors: %v", errs)
	}
	if moved != 1 {
		t.Fatalf("moved = %d, want 1", moved)
	}

	destPath := filepath.Join(toDir, "img-001.png")
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Fatalf("source file still exists at %s", srcPath)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading moved file: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("moved file content mismatch: got %q want %q", got, content)
	}

	var dbPath string
	if err := db.QueryRow(`SELECT path FROM vault_images WHERE id = 'img-001'`).Scan(&dbPath); err != nil {
		t.Fatalf("reading updated path: %v", err)
	}
	if dbPath != destPath {
		t.Fatalf("db path = %q, want %q", dbPath, destPath)
	}
}

func TestMoveFiles_SecondCallIsNoOp(t *testing.T) {
	db := newVaultTestDB(t)
	fromDir := t.TempDir()
	toDir := t.TempDir()

	srcPath := filepath.Join(fromDir, "img-001.png")
	if err := os.WriteFile(srcPath, []byte("x"), 0600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vault_images (id, path, filename, profile_id) VALUES ('img-001', ?, 'img-001.png', 'p1')`, srcPath); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	if moved, errs := MoveFiles(context.Background(), db, "p1", fromDir, toDir); moved != 1 || len(errs) != 0 {
		t.Fatalf("first move: moved=%d errs=%v", moved, errs)
	}

	// Re-run with the SAME fromDir — the row's path is now under toDir, not
	// fromDir, so this must be a clean no-op rather than an error.
	moved, errs := MoveFiles(context.Background(), db, "p1", fromDir, toDir)
	if len(errs) != 0 {
		t.Fatalf("second move errors: %v", errs)
	}
	if moved != 0 {
		t.Fatalf("second move moved = %d, want 0 (already moved)", moved)
	}
}

func TestMoveFiles_OnlyMovesMatchingProfile(t *testing.T) {
	db := newVaultTestDB(t)
	fromDir := t.TempDir()
	toDir := t.TempDir()

	pathA := filepath.Join(fromDir, "img-a.png")
	pathB := filepath.Join(fromDir, "img-b.png")
	os.WriteFile(pathA, []byte("a"), 0600)
	os.WriteFile(pathB, []byte("b"), 0600)
	db.Exec(`INSERT INTO vault_images (id, path, filename, profile_id) VALUES ('img-a', ?, 'img-a.png', 'p1')`, pathA)
	db.Exec(`INSERT INTO vault_images (id, path, filename, profile_id) VALUES ('img-b', ?, 'img-b.png', 'p2')`, pathB)

	moved, errs := MoveFiles(context.Background(), db, "p1", fromDir, toDir)
	if len(errs) != 0 || moved != 1 {
		t.Fatalf("moved=%d errs=%v, want moved=1 no errs", moved, errs)
	}
	if _, err := os.Stat(pathB); err != nil {
		t.Fatalf("profile p2's file was moved too: %v", err)
	}
}
