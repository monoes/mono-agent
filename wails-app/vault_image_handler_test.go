package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// /vault-image/<file> serves only files of the active profile, and never
// escapes the vault folder.
func TestVaultImageHandlerIsProfileScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vaultDir := filepath.Join(home, ".monoagent", "vault")
	if err := os.MkdirAll(vaultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"img-001.png", "img-002.png"} {
		if err := os.WriteFile(filepath.Join(vaultDir, f), []byte("png:"+f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestApp(t)
	a.setActiveProfileID("p1")
	if _, err := a.db.Exec(`INSERT INTO vault_images (id, seq, path, filename, profile_id) VALUES
		('img-001', 1, ?, 'img-001.png', 'p1'), ('img-002', 2, ?, 'img-002.png', 'p2')`,
		filepath.Join(vaultDir, "img-001.png"), filepath.Join(vaultDir, "img-002.png")); err != nil {
		t.Fatal(err)
	}
	h := vaultImageHandler(a)
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/vault-image/img-001.png"); code != 200 || body != "png:img-001.png" {
		t.Fatalf("own image: %d %q", code, body)
	}
	if code, _ := get("/vault-image/img-002.png"); code != 404 {
		t.Fatalf("another profile's image: %d, want 404", code)
	}
	if code, _ := get("/vault-image/../../etc/passwd"); code != 404 {
		t.Fatalf("traversal: %d, want 404", code)
	}
}
