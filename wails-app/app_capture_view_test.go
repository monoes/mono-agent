package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCapture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "inbox", "2026-09-22T11-00-08Z-google-com-search")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadCaptureViewReadsAllParts(t *testing.T) {
	dir := writeCapture(t, map[string]string{
		"meta.json":      `{"title":"Search Results","url":"https://www.google.com/search?q=x","capturedAt":"2026-09-22T11:00:08Z","wordCount":42}`,
		"readable.md":    "# Gastown\n\nThe original settlement.",
		"screenshot.png": "\x89PNG fake",
	})
	v, err := readCaptureView(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "Search Results" || v.URL != "https://www.google.com/search?q=x" || v.WordCount != 42 {
		t.Errorf("meta not read: %+v", v)
	}
	if !strings.Contains(v.Readable, "Gastown") {
		t.Errorf("readable = %q", v.Readable)
	}
	if !strings.HasPrefix(v.Screenshot, "data:image/png;base64,") {
		t.Errorf("screenshot = %.40q", v.Screenshot)
	}
}

// A page with no readable text (a claude.ai artifact, an app) still has a
// screenshot, and that alone must be enough to preview it.
func TestReadCaptureViewToleratesMissingParts(t *testing.T) {
	dir := writeCapture(t, map[string]string{
		"meta.json":      `{"title":"Jev Picker Plan","url":"https://claude.ai/artifact/x","wordCount":0}`,
		"screenshot.png": "\x89PNG fake",
	})
	v, err := readCaptureView(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v.Readable != "" || v.Screenshot == "" {
		t.Errorf("want screenshot only, got readable=%q screenshot=%t", v.Readable, v.Screenshot != "")
	}
}

func TestReadCaptureViewNeedsMeta(t *testing.T) {
	dir := writeCapture(t, map[string]string{"readable.md": "text"})
	if _, err := readCaptureView(dir); err == nil {
		t.Fatal("a folder without meta.json was read as a capture")
	}
}
