// wails-app/app_files.go
package main

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
)

// documentPath resolves id to its on-disk path, scoped to the active
// profile — the same lookup GetVaultImageData uses for vault_images.
func (a *App) documentPath(id string) (string, error) {
	if a.db == nil {
		return "", fmt.Errorf("database not available")
	}
	var path string
	err := a.db.QueryRow(`SELECT path FROM vault_documents WHERE id = ? AND profile_id = ?`, id, a.getActiveProfileID()).Scan(&path)
	if err != nil {
		return "", fmt.Errorf("document %q not found: %w", id, err)
	}
	return path, nil
}

// GetProfileDocumentData reads a vault document from disk and returns it as
// a base64 data URL (e.g. "data:application/pdf;base64,..."), the same
// technique GetVaultImageData uses for images — the reliable way to load
// local file content into the Wails WebView for binary preview (images,
// PDFs) without a file:// URL, which Wails' BrowserOpenURL explicitly
// rejects and which the webview's CSP may also block.
func (a *App) GetProfileDocumentData(id string) (string, error) {
	path, err := a.documentPath(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading document file: %w", err)
	}
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// GetProfileDocumentText reads a vault document from disk as UTF-8 text,
// for file types previewed as plain text/source (txt, md, html, csv,
// json, ...) rather than as a binary data URL.
func (a *App) GetProfileDocumentText(id string) (string, error) {
	path, err := a.documentPath(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading document file: %w", err)
	}
	return string(data), nil
}

// OpenPathWithOS hands path to the operating system's default file-open
// mechanism — the same as double-clicking it in a file manager. Used as the
// fallback when no in-app viewer exists for a file's type.
func (a *App) OpenPathWithOS(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("file not found: %w", err)
	}
	name, args := openFileCommand(goruntime.GOOS, path)
	// Start, not Run: the launched app (e.g. a text editor or Office) may
	// stay open indefinitely, so this only reports launch failures (missing
	// binary, ...), not the opened app's own exit code — same fire-and-forget
	// shape as app_update.go's install scripts.
	return exec.Command(name, args...).Start()
}

// openFileCommand returns the OS-appropriate command name and arguments to
// open path with its default associated application: "open" on darwin,
// "cmd /c start" on windows (an empty title arg is required so `start`
// doesn't mistake a quoted path for the window title), and "xdg-open"
// everywhere else. Split out from OpenPathWithOS, mirroring
// revealFolderCommand in app.go, so tests can exercise the per-GOOS command
// selection without launching a real application.
func openFileCommand(goos, path string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{path}
	case "windows":
		return "cmd", []string{"/c", "start", "", path}
	default:
		return "xdg-open", []string{path}
	}
}
