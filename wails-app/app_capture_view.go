// wails-app/app_capture_view.go
package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CaptureView is everything the Documents page shows for one browser
// capture: the page's readable text and its screenshot, side by side.
// A capture's primary file is often page.mhtml (no readable text was
// found), which no in-app viewer can show and which the OS tends to hand
// to a text editor — so the capture is previewed from its parts instead.
type CaptureView struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	CapturedAt string `json:"captured_at"`
	WordCount  int    `json:"word_count"`
	Readable   string `json:"readable"`   // readable.md, "" when absent
	Screenshot string `json:"screenshot"` // data URL of screenshot.png, "" when absent
}

// maxCaptureReadableBytes caps the text sent to the webview; a capture's
// readable.md is an article, so anything past this is not one.
const maxCaptureReadableBytes = 2 * 1024 * 1024

// GetCaptureView loads a capture document's parts, scoped to the active
// profile like every other document read.
func (a *App) GetCaptureView(id string) (*CaptureView, error) {
	if a.db == nil {
		return nil, fmt.Errorf("database not available")
	}
	var dir sql.NullString
	err := a.db.QueryRow(`SELECT capture_dir FROM vault_documents WHERE id = ? AND profile_id = ?`, id, a.getActiveProfileID()).Scan(&dir)
	if err != nil {
		return nil, fmt.Errorf("document %q not found: %w", id, err)
	}
	if !dir.Valid || dir.String == "" {
		return nil, fmt.Errorf("document %q is not a browser capture", id)
	}
	return readCaptureView(dir.String)
}

// readCaptureView reads one capture envelope. Missing parts are left empty
// rather than failing: a capture keeps whatever the page allowed.
func readCaptureView(dir string) (*CaptureView, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("reading capture: %w", err)
	}
	var meta struct {
		Title      string `json:"title"`
		URL        string `json:"url"`
		CapturedAt string `json:"capturedAt"`
		WordCount  int    `json:"wordCount"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("reading capture meta.json: %w", err)
	}
	v := &CaptureView{Title: meta.Title, URL: meta.URL, CapturedAt: meta.CapturedAt, WordCount: meta.WordCount}

	if text, err := readCapped(filepath.Join(dir, "readable.md"), maxCaptureReadableBytes); err == nil {
		v.Readable = string(text)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if png, err := readCapped(filepath.Join(dir, "screenshot.png"), maxInlinePreviewBytes); err == nil {
		v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return v, nil
}

func readCapped(path string, limit int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s is too large to preview (%d MB)", filepath.Base(path), fi.Size()/1024/1024)
	}
	return os.ReadFile(path)
}
