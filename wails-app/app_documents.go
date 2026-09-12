// wails-app/app_documents.go
package main

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/vault"
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
	Indexed       bool   `json:"indexed"`
	IndexError    string `json:"index_error"`
	Stale         bool   `json:"stale"`
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
		ID, Path, Filename, Source, ApplicationID, CreatedAt, IndexError string
		SizeBytes                                                        int64
		Indexed                                                          bool
		Stale                                                            bool
	}
	if err := a.runMonoCLI("", &raw, "profile", "documents", "list"); err != nil {
		return nil, err
	}
	out := make([]ProfileDocument, 0, len(raw))
	for _, d := range raw {
		out = append(out, ProfileDocument{
			ID: d.ID, Filename: d.Filename, Path: d.Path, SizeBytes: d.SizeBytes,
			Source: d.Source, ApplicationID: d.ApplicationID, CreatedAt: d.CreatedAt,
			Indexed: d.Indexed, IndexError: d.IndexError, Stale: d.Stale,
		})
	}
	return out, nil
}

// GetProfileDocument returns one document by id, scoped to the active
// profile — a single-row counterpart to ListProfileDocuments for callers
// (chatArtifacts.js's resolveArtifact) that only need to re-validate one
// id rather than pay for and scan the whole vault. Mirrors GetWorkflow's
// shape (app_workflows.go): direct DB access, no CLI subprocess hop,
// nil+error on not-found rather than an empty result — a.db is the same
// handle app_documents_watch.go already calls internal/vault with
// directly, so this needs no new plumbing.
func (a *App) GetProfileDocument(id string) (*ProfileDocument, error) {
	doc, err := vault.GetDocument(context.Background(), a.db, a.getActiveProfileID(), id)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("document %s not found", id)
	}
	return &ProfileDocument{
		ID: doc.ID, Filename: doc.Filename, Path: doc.Path, SizeBytes: doc.SizeBytes,
		Source: doc.Source, ApplicationID: doc.ApplicationID, CreatedAt: doc.CreatedAt,
		Indexed: doc.Indexed, IndexError: doc.IndexError, Stale: doc.Stale,
	}, nil
}

// UploadResult is UploadProfileDocument's return value.
type UploadResult struct {
	ID         string `json:"id"`
	Indexed    bool   `json:"indexed"`
	IndexError string `json:"index_error"`
}

// UploadProfileDocument registers path in the vault and indexes it for
// knowledge search. source defaults to "upload" (the CLI's own default)
// if empty. The upload itself succeeds even if indexing fails (e.g.
// monomind isn't installed or this profile isn't monomind-init'd) --
// Indexed/IndexError report that outcome so the caller can show it.
func (a *App) UploadProfileDocument(path, source string) (*UploadResult, error) {
	args := []string{"profile", "upload-document", path}
	if source != "" {
		args = append(args, "--source", source)
	}
	var result UploadResult
	if err := a.runMonoCLI("", &result, args...); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteProfileDocument removes a document from the vault.
func (a *App) DeleteProfileDocument(id string) error {
	return a.runMonoCLI("", nil, "profile", "documents", "rm", id)
}

// IndexProfileDocument runs (or re-runs) knowledge_ingest for a single
// already-tracked document -- the on-demand action for a "Not indexed" or
// "Stale" row; never triggered automatically by discovery. Reuses
// UploadResult's shape exactly so the frontend can treat a manual index
// and an upload's own immediate index attempt identically.
func (a *App) IndexProfileDocument(id string) (*UploadResult, error) {
	var result UploadResult
	if err := a.runMonoCLI("", &result, "profile", "documents", "index", id); err != nil {
		return nil, err
	}
	return &result, nil
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
