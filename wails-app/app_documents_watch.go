// wails-app/app_documents_watch.go
package main

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/capturedocs"
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
	if a.capWatcher != nil {
		a.capWatcher.Stop()
		a.capWatcher = nil
	}

	profileID := a.getActiveProfileID()
	a.startCaptureWatcher(profileID)

	root := a.documentRootForActiveProfile()
	if root == "" {
		return
	}

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

// startCaptureWatcher watches the profile's browser-capture inbox, which
// the folder scan above skips (it is under .monomind/). It only signals:
// the page answers documents:changed by re-fetching the list, and
// `profile documents list` is what syncs captures into the vault, so the
// GUI never writes capture rows itself. Caller holds docWatchMu.
func (a *App) startCaptureWatcher(profileID string) {
	inbox, err := capture.ProfileInbox(profileID)
	if err != nil {
		return // no usable profile id, so no inbox to watch
	}
	w := capturedocs.NewWatcher(inbox, 0, func() {
		runtime.EventsEmit(a.ctx, "documents:changed", map[string]interface{}{"profileID": profileID, "captures": true})
	})
	w.Start()
	a.capWatcher = w
}
