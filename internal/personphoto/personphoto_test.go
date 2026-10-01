package personphoto

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testhome"
	"github.com/monoes/mono-agent/internal/vault"
)

func TestMain(m *testing.M) { testhome.Main(m) }

// a 1x1 PNG
const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func newDB(t *testing.T) *storage.Database {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	return db
}

func TestLocalizeSavesPhotoAndKeepsSource(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(pngB64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone.jpg" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/page.jpg" {
			w.Write([]byte("<html>not an image</html>"))
			return
		}
		w.Write(png)
	}))
	defer srv.Close()
	t.Cleanup(vault.Wait)

	db := newDB(t)
	for u, url := range map[string]string{"ada": srv.URL + "/ada.jpg", "gone": srv.URL + "/gone.jpg", "html": srv.URL + "/page.jpg"} {
		if _, err := db.DB.Exec(`INSERT INTO people (id, platform_username, platform, image_url, profile_id) VALUES (?, ?, 'X', ?, 'default')`, "id-"+u, u, url); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	saved, errs := Localize(ctx, db.DB, "default", nil)
	if saved != 1 || len(errs) != 2 {
		t.Fatalf("saved %d, errs %v; want 1 saved, 2 errors", saved, errs)
	}

	var img, src, imgID string
	db.DB.QueryRow(`SELECT image_url, json_extract(profile_details,'$.photo_source_url'), json_extract(profile_details,'$.photo_image_id') FROM people WHERE id='id-ada'`).Scan(&img, &src, &imgID)
	if !strings.HasPrefix(img, "/vault-image/") || src != srv.URL+"/ada.jpg" || img != "/vault-image/"+imgID {
		t.Fatalf("ada: image_url %q source %q id %q", img, src, imgID)
	}
	path, ok, err := vault.ImagePathInProfile(ctx, db.DB, "default", imgID)
	if err != nil || !ok {
		t.Fatalf("vault image %s: %v %v", imgID, ok, err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != string(png) {
		t.Fatalf("saved file differs: %v", err)
	}
	var gone string
	db.DB.QueryRow(`SELECT image_url FROM people WHERE id='id-gone'`).Scan(&gone)
	if gone != srv.URL+"/gone.jpg" {
		t.Fatalf("unfetchable photo lost its URL: %q", gone)
	}

	// A re-scrape sets the remote URL again: the new copy replaces the old one.
	db.DB.Exec(`UPDATE people SET image_url = ? WHERE id='id-ada'`, srv.URL+"/ada-new.jpg")
	if saved, errs := Localize(ctx, db.DB, "default", []Ref{{"X", "ada"}}); saved != 1 || len(errs) != 0 {
		t.Fatalf("second run: %d %v", saved, errs)
	}
	var n int
	db.DB.QueryRow(`SELECT COUNT(*) FROM vault_images WHERE source = ?`, Source).Scan(&n)
	if n != 1 {
		t.Fatalf("%d profile-photo images after a re-save; want the old one removed", n)
	}
}
