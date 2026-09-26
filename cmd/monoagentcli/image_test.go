package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/vault"
)

// newImageCLITestDB is a migrated DB with two images in "default" (one in
// a file on disk) and one in another profile, under a temp HOME.
func newImageCLITestDB(t *testing.T) (*globalConfig, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no monomind: Register's knowledge-graph sync is a no-op
	t.Cleanup(vault.Wait)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "i.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	onDisk := filepath.Join(dir, "img-002.png")
	if err := os.WriteFile(onDisk, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`
		INSERT INTO vault_images (id, seq, path, filename, size_bytes, source, workflow_id, label, profile_id, created_at)
		VALUES ('img-001', 1, '/gone/img-001.png', 'img-001.png', 10, 'gemini', 'wf-banner', NULL, 'default', '2026-09-26 10:00:00'),
		       ('img-002', 2, ?, 'img-002.png', 9, 'upload', NULL, 'Logo 100%', 'default', '2026-09-26 11:00:00'),
		       ('img-003', 3, '/x/img-003.png', 'img-003.png', 5, 'upload', NULL, 'Logo', 'work', '2026-09-26 12:00:00');
		INSERT INTO profiles (id, name, created_at) VALUES ('work', 'Work', '2026-01-01'), ('empty', 'Empty', '2026-01-01')`, onDisk); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}, dir
}

func runImage(t *testing.T, cfg *globalConfig, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newImageCmd(cfg)
	cmd.SilenceUsage = true
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func imageIDs(t *testing.T, out string) []string {
	t.Helper()
	var rows []vault.ImageEntry
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out)
	}
	ids := []string{}
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestImageListIsProfileScopedAndSnakeCase(t *testing.T) {
	cfg, _ := newImageCLITestDB(t)
	out, err := runImage(t, cfg, "list")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(imageIDs(t, out), ","); got != "img-002,img-001" {
		t.Fatalf("list = %s", got)
	}
	var rows []map[string]any
	_ = json.Unmarshal([]byte(out), &rows)
	for _, k := range []string{"id", "seq", "path", "filename", "size_bytes", "source", "workflow_id", "execution_id", "label", "created_at", "url"} {
		if _, ok := rows[1][k]; !ok {
			t.Errorf("missing %q in %v", k, rows[1])
		}
	}
	if rows[1]["url"] != "/vault-image/img-001.png" || rows[1]["label"] != "" || rows[1]["workflow_id"] != "wf-banner" {
		t.Errorf("row = %v", rows[1])
	}
	if out, _ := runImage(t, cfg, "list", "--limit", "1"); strings.Join(imageIDs(t, out), ",") != "img-002" {
		t.Errorf("--limit 1 = %s", out)
	}
	if _, err := runImage(t, cfg, "list", "--limit", "0"); exitCode(err) != 3 {
		t.Errorf("--limit 0: exit %d", exitCode(err))
	}

	cfg.ProfileID = "work"
	if out, _ := runImage(t, cfg, "list"); strings.Join(imageIDs(t, out), ",") != "img-003" {
		t.Errorf("work's list = %s", out)
	}
	cfg.ProfileID = "empty"
	if out, err := runImage(t, cfg, "list"); err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty profile list = %q, %v", out, err)
	}
}

func TestImageSearch(t *testing.T) {
	cfg, _ := newImageCLITestDB(t)
	cases := map[string]string{
		"logo":   "img-002", // label, case-insensitive; not work's img-003
		"banner": "img-001", // workflow id
		"gemini": "img-001", // source
		"100%":   "img-002", // % is literal
		"_":      "",        // _ is literal: nothing has an underscore
		"":       "img-002,img-001",
	}
	for q, want := range cases {
		out, err := runImage(t, cfg, "search", q)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if got := strings.Join(imageIDs(t, out), ","); got != want {
			t.Errorf("search %q = %q, want %q", q, got, want)
		}
	}
}

func TestImageGetStatsAndNotFound(t *testing.T) {
	cfg, _ := newImageCLITestDB(t)
	out, err := runImage(t, cfg, "get", "img-002")
	if err != nil {
		t.Fatal(err)
	}
	var im vault.ImageEntry
	if err := json.Unmarshal([]byte(out), &im); err != nil || im.ID != "img-002" || im.Label != "Logo 100%" || im.Seq != 2 {
		t.Fatalf("get = %+v, %v", im, err)
	}
	out, err = runImage(t, cfg, "stats")
	if err != nil || strings.Join(strings.Fields(out), "") != `{"count":2,"total_bytes":19}` {
		t.Fatalf("stats = %s, %v", out, err)
	}
	for _, args := range [][]string{
		{"get", "img-003"}, // another profile's
		{"data", "nope"},
		{"label", "nope", "x"},
		{"delete", "img-003"},
		{"export", "nope", filepath.Join(t.TempDir(), "x.png")},
	} {
		if _, err := runImage(t, cfg, args...); exitCode(err) != 2 {
			t.Errorf("%v: exit %d (%v)", args, exitCode(err), err)
		}
	}
}

func TestImageData(t *testing.T) {
	cfg, _ := newImageCLITestDB(t)
	out, err := runImage(t, cfg, "data", "img-002")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID, MimeType string
		SizeBytes    int    `json:"size_bytes"`
		DataURL      string `json:"data_url"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	if got.DataURL != want || got.SizeBytes != 9 || !strings.Contains(out, `"mime_type": "image/png"`) {
		t.Fatalf("data = %s", out)
	}
	// A row whose file is gone is an ordinary error, not "not found".
	if _, err := runImage(t, cfg, "data", "img-001"); err == nil || exitCode(err) == 2 {
		t.Fatalf("missing file: %v", err)
	}
}

func TestImageAddLabelExportDelete(t *testing.T) {
	cfg, dir := newImageCLITestDB(t)
	src := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(src, []byte("jpeg!"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runImage(t, cfg, "add", src, "--label", "Holiday")
	if err != nil {
		t.Fatal(err)
	}
	var im vault.ImageEntry
	if err := json.Unmarshal([]byte(out), &im); err != nil {
		t.Fatal(err)
	}
	// seq is global across profiles (work's img-003 exists), ids follow it.
	if im.ID != "img-004" || im.Seq != 4 || im.Filename != "img-004.jpg" || im.Label != "Holiday" || im.Source != "upload" || im.SizeBytes != 5 {
		t.Fatalf("add = %+v", im)
	}
	if b, err := os.ReadFile(im.Path); err != nil || string(b) != "jpeg!" || filepath.Base(im.Path) != "img-004.jpg" {
		t.Fatalf("vault copy %s: %q %v", im.Path, b, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("original must stay: %v", err)
	}

	if _, err := runImage(t, cfg, "add", filepath.Join(dir, "missing.png")); exitCode(err) != 3 {
		t.Errorf("add missing file: exit %d", exitCode(err))
	}
	if _, err := runImage(t, cfg, "add", dir); exitCode(err) != 3 {
		t.Errorf("add a directory: exit %d", exitCode(err))
	}

	if out, err := runImage(t, cfg, "label", "img-004", "Beach"); err != nil || !strings.Contains(out, `"label": "Beach"`) {
		t.Fatalf("label = %s, %v", out, err)
	}
	if _, err := runImage(t, cfg, "label", "img-004"); err != nil {
		t.Fatal(err)
	}
	out, _ = runImage(t, cfg, "get", "img-004")
	if err := json.Unmarshal([]byte(out), &im); err != nil || im.Label != "" {
		t.Fatalf("after clearing the label: %+v", im)
	}

	dest := filepath.Join(t.TempDir(), "out.jpg")
	out, err = runImage(t, cfg, "export", "img-004", dest)
	if err != nil || !strings.Contains(out, `"path": "`+dest+`"`) {
		t.Fatalf("export = %s, %v", out, err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "jpeg!" {
		t.Fatalf("exported %q", b)
	}
	if _, err := runImage(t, cfg, "export", "img-004", t.TempDir()); exitCode(err) != 3 {
		t.Errorf("export to a directory: exit %d", exitCode(err))
	}

	if _, err := runImage(t, cfg, "delete", "img-004"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(im.Path); !os.IsNotExist(err) {
		t.Fatalf("vault file still there: %v", err)
	}
	if _, err := runImage(t, cfg, "get", "img-004"); exitCode(err) != 2 {
		t.Fatalf("get after delete: exit %d", exitCode(err))
	}
	// A row whose file is already gone still deletes.
	if _, err := runImage(t, cfg, "rm", "img-001"); err != nil {
		t.Fatal(err)
	}
}
