// wails-app/app_documents.go
package main

import (
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ProfileDocument mirrors internal/vault.DocumentEntry's fields (that
// struct has no json tags -> PascalCase on the wire), remapped to a
// stable lowercase contract for the frontend.
type ProfileDocument struct {
	ID            string `json:"id"`
	Filename      string `json:"filename"`
	Path          string `json:"path"`
	SizeBytes     int64  `json:"size_bytes"`
	Source        string `json:"source"`
	ApplicationID string `json:"application_id"`
	CreatedAt     string `json:"created_at"`
}

// KnowledgeSearchResult mirrors internal/monomind.KnowledgeResult (also
// tagless -> PascalCase on the wire).
type KnowledgeSearchResult struct {
	Path    string  `json:"path"`
	Excerpt string  `json:"excerpt"`
	Score   float64 `json:"score"`
}

// ListProfileDocuments lists every document uploaded to the active
// profile's vault.
func (a *App) ListProfileDocuments() ([]ProfileDocument, error) {
	var raw []struct {
		ID, Path, Filename, Source, ApplicationID, CreatedAt string
		SizeBytes                                            int64
	}
	if err := a.runMonoCLI("", &raw, "profile", "documents", "list"); err != nil {
		return nil, err
	}
	out := make([]ProfileDocument, 0, len(raw))
	for _, d := range raw {
		out = append(out, ProfileDocument{
			ID: d.ID, Filename: d.Filename, Path: d.Path, SizeBytes: d.SizeBytes,
			Source: d.Source, ApplicationID: d.ApplicationID, CreatedAt: d.CreatedAt,
		})
	}
	return out, nil
}

// UploadProfileDocument registers path in the vault and indexes it for
// knowledge search. source defaults to "upload" (the CLI's own default)
// if empty.
func (a *App) UploadProfileDocument(path, source string) (string, error) {
	args := []string{"profile", "upload-document", path}
	if source != "" {
		args = append(args, "--source", source)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := a.runMonoCLI("", &result, args...); err != nil {
		return "", err
	}
	return result.ID, nil
}

// DeleteProfileDocument removes a document from the vault.
func (a *App) DeleteProfileDocument(id string) error {
	return a.runMonoCLI("", nil, "profile", "documents", "rm", id)
}

// SearchProfileKnowledge searches indexed profile documents (the same
// search chat uses automatically).
func (a *App) SearchProfileKnowledge(query string) ([]KnowledgeSearchResult, error) {
	var raw []struct {
		Path, Excerpt string
		Score         float64
	}
	if err := a.runMonoCLI("", &raw, "profile", "search-knowledge", query); err != nil {
		return nil, err
	}
	out := make([]KnowledgeSearchResult, 0, len(raw))
	for _, r := range raw {
		out = append(out, KnowledgeSearchResult{Path: r.Path, Excerpt: r.Excerpt, Score: r.Score})
	}
	return out, nil
}

// OpenAnyFilePicker opens a native file picker with no extension filter
// (profile documents can be PDF, DOCX, plain text, etc.) and returns the
// selected path (empty string if cancelled).
func (a *App) OpenAnyFilePicker(title string) string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
	if err != nil {
		return ""
	}
	return path
}
