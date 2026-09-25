// wails-app/app_images_watch.go
package main

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/imagescan"
	"github.com/monoes/mono-agent/internal/vault"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// restartImageWatcher stops any existing image watcher and starts a new one
// scoped to the active profile's project folder. Mirrors restartDocumentWatcher
// in shape and call sites (startup, MoveProfileFolder, SwitchProfile, shutdown).
func (a *App) restartImageWatcher() {
	a.imgWatchMu.Lock()
	defer a.imgWatchMu.Unlock()

	if a.imgWatcher != nil {
		a.imgWatcher.Stop()
		a.imgWatcher = nil
	}

	profileID := a.getActiveProfileID()
	root := a.documentRootForActiveProfile()
	if root == "" {
		return
	}

	w := imagescan.NewWatcher(root, 0, func(files []imagescan.FileInfo) {
		found := make([]vault.DiscoveredFile, len(files))
		for i, f := range files {
			found[i] = vault.DiscoveredFile{Path: f.Path, Filename: f.Filename, SizeBytes: f.SizeBytes}
		}
		added, removed, errs := vault.ReconcileDiscoveredImages(context.Background(), a.db, profileID, found)
		for _, e := range errs {
			a.emitLog("SYSTEM", "WARN", fmt.Sprintf("profile %s: image discovery: %v", profileID, e))
		}
		if a.ctx != nil && (added > 0 || removed > 0 || len(errs) > 0) {
			runtime.EventsEmit(a.ctx, "images:changed", map[string]interface{}{
				"profileID": profileID,
				"added":     added,
				"removed":   removed,
			})
		}
	})
	w.Start()
	a.imgWatcher = w
}
