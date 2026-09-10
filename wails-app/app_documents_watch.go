// wails-app/app_documents_watch.go
package main

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/docscan"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/vault"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// documentRootForActiveProfile resolves the directory the document watcher
// scans -- the active profile's whole folder. Returns
// profiledir.Root(a.db, a.getActiveProfileID()) UNMODIFIED (no
// Abs/EvalSymlinks) -- see internal/docscan.Scan's doc comment for why:
// monomind.IngestDocument calls profiledir.Root independently at index
// time and its path-traversal guard requires the two to agree
// byte-for-byte.
func (a *App) documentRootForActiveProfile() string {
	return profiledir.Root(a.db, a.getActiveProfileID())
}

// restartDocumentWatcher stops any existing document watcher and starts a
// new one scoped to the active profile's folder. Mirrors restartOrgWatcher
// exactly in shape and call sites (startup, MoveProfileFolder,
// SwitchProfile, shutdown).
func (a *App) restartDocumentWatcher() {
	a.docWatchMu.Lock()
	defer a.docWatchMu.Unlock()

	if a.docWatcher != nil {
		a.docWatcher.Stop()
		a.docWatcher = nil
	}

	root := a.documentRootForActiveProfile()
	if root == "" {
		return
	}
	profileID := a.getActiveProfileID()

	w := docscan.NewWatcher(root, 0, func(files []docscan.FileInfo) {
		found := make([]vault.DiscoveredFile, len(files))
		for i, f := range files {
			found[i] = vault.DiscoveredFile{Path: f.Path, Filename: f.Filename, SizeBytes: f.SizeBytes}
		}
		added, removed, errs := vault.ReconcileDiscoveredDocuments(context.Background(), a.db, profileID, found)
		for _, e := range errs {
			a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: document discovery: %v", profileID, e))
		}
		runtime.EventsEmit(a.ctx, "documents:changed", map[string]interface{}{"profileID": profileID, "added": added, "removed": removed})
	})
	w.Start()
	a.docWatcher = w
}
