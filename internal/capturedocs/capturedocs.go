// Package capturedocs lists a profile's browser captures as documents.
//
// The extension bridge writes each capture into the profile's own inbox
// (capture.ProfileInbox: <profile>/.monomind/inbox/<ts>-<slug>/), a
// dot-directory the profile-folder document scan deliberately skips. This
// package is the bridge between the two: it reads that inbox and
// reconciles it into vault_documents, one row per capture, so a capture
// shows up in the Documents list and can be opened, indexed and deleted
// through the same paths as any other document.
//
// Captures saved to the shared inbox (no profile named) are not listed for
// any profile: keeping one profile's pages out of another's documents is
// the reason captures carry a profile at all.
package capturedocs

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/vault"
)

// primaryPreference is the order artifacts are chosen in for the row a
// capture is listed, opened and indexed as: the readable text first (it
// previews in-app and indexes well), then the byte-fidelity renderings,
// and the screenshot only when nothing else was captured.
//
// summary.md is deliberately NOT preferred over readable.md, though it
// previews nicely. It is written minutes after the capture lands, so
// preferring it would move the row's path once the summary arrived and
// re-index the capture as a few hundred words standing in for the whole
// page — search wants the full text, and the viewer shows the summary on
// its own tab anyway. transcript.md and summary.md rank only above the
// screenshot, for a capture that has no page text at all.
var primaryPreference = []string{
	capture.ArtifactReadable,
	capture.ArtifactPDF,
	capture.ArtifactMHTML,
	capture.ArtifactHTML,
	"transcript.md",
	"summary.md",
	capture.ArtifactScreenshot,
}

// PrimaryArtifact picks the artifact a capture is listed as, from the
// names in its envelope directory. An envelope holding none of the
// canonical names falls back to its first artifact in sorted order; one
// with no artifacts at all returns "".
func PrimaryArtifact(artifacts []string) string {
	have := make(map[string]bool, len(artifacts))
	for _, a := range artifacts {
		have[a] = true
	}
	for _, name := range primaryPreference {
		if have[name] {
			return name
		}
	}
	if len(artifacts) > 0 {
		return artifacts[0] // capture.List sorts them
	}
	return ""
}

// FromEntries maps inbox entries to vault rows, skipping any envelope with
// no artifact to open.
func FromEntries(entries []capture.Entry) []vault.CaptureDocument {
	out := make([]vault.CaptureDocument, 0, len(entries))
	for _, e := range entries {
		primary := PrimaryArtifact(e.Artifacts)
		if primary == "" {
			continue
		}
		path := filepath.Join(e.Path, primary)
		var size int64
		if fi, err := os.Stat(path); err == nil {
			size = fi.Size()
		}
		out = append(out, vault.CaptureDocument{
			Dir:        e.Path,
			Path:       path,
			Title:      strings.TrimSpace(e.Title),
			URL:        e.URL,
			Source:     e.Meta.Source,
			CapturedAt: sqlTime(e.CapturedAt),
			SizeBytes:  size,
		})
	}
	return out
}

// sqlTime renders a capture's RFC 3339 capturedAt in created_at's shape
// ("2006-01-02 15:04:05", UTC). An unparseable value returns "", which
// the vault replaces with the time of registration.
func sqlTime(s string) string {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t.UTC().Format("2006-01-02 15:04:05")
		}
	}
	return ""
}

// Sync reconciles profileID's inbox into its document rows. An inbox that
// does not exist yet is not an error: it has no captures. No profile at
// all is a no-op — the shared inbox belongs to nobody's document list.
func Sync(ctx context.Context, db *sql.DB, profileID string) (added, removed int, errs []error) {
	if strings.TrimSpace(profileID) == "" {
		return 0, 0, nil
	}
	inbox, err := capture.ProfileInbox(profileID)
	if err != nil {
		return 0, 0, []error{err}
	}
	return SyncInbox(ctx, db, profileID, inbox)
}

// SyncInbox is Sync against an explicit inbox directory.
func SyncInbox(ctx context.Context, db *sql.DB, profileID, inbox string) (added, removed int, errs []error) {
	entries, err := capture.List(inbox)
	if err != nil {
		// An unreadable inbox is not an empty one: reconciling against
		// nothing would drop every capture row.
		return 0, 0, []error{err}
	}
	// capture.List is newest first; register oldest first so document
	// ids (and the list's default seq order) follow capture order.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return vault.ReconcileCaptureDocuments(ctx, db, profileID, FromEntries(entries))
}
