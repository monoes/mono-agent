// Package captureindex makes a profile's browser captures searchable.
//
// A capture lands in the profile's inbox (capture.ProfileInbox) and
// capturedocs turns it into a document row, but a row is not knowledge:
// "Ask your brain" and chat search read monomind's store, and nothing put
// the capture there. This package is that step. It ingests every capture
// row that is not indexed (or has changed since it was) into the profile's
// own capture store — monomind's `profile:<id>` scope (monomind.CaptureScope)
// — and records the outcome on the row, so the Documents list shows
// Indexed or the error.
//
// Three callers share it: the extension bridge after every capture it
// writes (Queue), `profile documents index --all` (the backfill), and
// `profile documents index <id>` for one capture row.
//
// Two processes can run a pass for one profile at once (the daemon's bridge
// and a backfill from a terminal), so a pass holds a per-profile lock and
// reads the rows only once it has it: the second pass then sees what the
// first one recorded and does not ingest the same capture again.
package captureindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/capturedocs"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/vault"
)

// IngestFunc indexes one capture file into profileID's capture store.
type IngestFunc func(ctx context.Context, profileID, path string) error

// Indexer runs indexing passes. The zero value ingests through monomind.
type Indexer struct {
	// Ingest replaces monomind.IngestCapture (tests).
	Ingest IngestFunc
	// Companions reports whether the installed monomind can take a
	// capture's companion documents (CompanionArtifacts) beside its
	// primary one. Nil asks monomind (monomind.SupportsCaptureCompanions).
	Companions func(ctx context.Context) bool
}

// CompanionArtifacts are the text documents a capture can carry beside
// the one it is listed and indexed as: a video's transcript (where what
// was SAID is — the page text is only the description) and the AI summary
// the bridge writes minutes after the capture lands. Each is ingested as a
// document of its own, from the same envelope, so its provenance is the
// page's.
var CompanionArtifacts = []string{"transcript.md", "summary.md"}

func (ix *Indexer) companions(ctx context.Context) bool {
	if ix != nil && ix.Companions != nil {
		return ix.Companions(ctx)
	}
	return monomind.SupportsCaptureCompanions(ctx)
}

func (ix *Indexer) ingest() IngestFunc {
	if ix != nil && ix.Ingest != nil {
		return ix.Ingest
	}
	return monomind.IngestCapture
}

// Options narrows a pass.
type Options struct {
	// RetryFailed also retries rows whose last attempt recorded an error.
	// Without it a failed row waits for a retry pass, so a capture monomind
	// cannot ingest is not re-run on every capture that lands after it.
	RetryFailed bool
	// IDs limits the pass to these document ids.
	IDs []string
	// Force re-indexes the rows in IDs even when they are up to date (the
	// Index button on a row that already says Indexed).
	Force bool
}

// Outcome is what a pass did with one capture row.
type Outcome struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Path    string `json:"path"`
	Indexed bool   `json:"indexed"`
	Error   string `json:"error,omitempty"`
}

// Report sums up one pass over one profile.
type Report struct {
	Profile string `json:"profile"`
	// Indexed and Failed count this pass's attempts; UpToDate counts the
	// capture rows that needed nothing, Waiting the failed rows left for a
	// retry pass.
	Indexed    int       `json:"indexed"`
	Failed     int       `json:"failed"`
	UpToDate   int       `json:"up_to_date"`
	Waiting    int       `json:"waiting"`
	Outcomes   []Outcome `json:"outcomes"`
	SyncErrors []string  `json:"sync_errors,omitempty"`
}

// NeedsIndex reports whether a pass should ingest d's primary document: a
// capture row that is not indexed, or was indexed from a file that has
// changed since. A row whose last attempt failed waits for a retry pass.
func NeedsIndex(d vault.DocumentEntry, retryFailed bool) bool {
	if d.CaptureDir == "" {
		return false
	}
	if d.Indexed && !d.Stale {
		return false
	}
	return d.IndexError == "" || retryFailed
}

// companionsDue lists d's companion documents that are new or changed
// since they were last ingested (per state), or nil.
func companionsDue(d vault.DocumentEntry, state indexState) []string {
	var due []string
	for _, name := range CompanionArtifacts {
		path := filepath.Join(d.CaptureDir, name)
		if path == d.Path {
			continue // the row's own document
		}
		sig := fileSig(path)
		if sig == "" {
			continue
		}
		if state[filepath.Base(d.CaptureDir)][name] != sig {
			due = append(due, name)
		}
	}
	return due
}

// IndexProfile brings profileID's capture rows up to date with its inbox
// (capturedocs.Sync) and ingests every row that needs it. An error is
// returned only when the pass could not run at all; per-capture failures
// are in the report and on the rows.
func (ix *Indexer) IndexProfile(ctx context.Context, db *sql.DB, profileID string, opts Options) (*Report, error) {
	id := strings.TrimSpace(profileID)
	if !profiledir.ValidProfileID(id) {
		return nil, fmt.Errorf("captureindex: unusable profile id %q", profileID)
	}
	unlock, err := lockProfile(id)
	if err != nil {
		return nil, err
	}
	defer unlock()

	report := &Report{Profile: id, Outcomes: []Outcome{}}
	_, _, syncErrs := capturedocs.Sync(ctx, db, id)
	for _, e := range syncErrs {
		report.SyncErrors = append(report.SyncErrors, e.Error())
	}
	docs, err := vault.ListDocuments(ctx, db, id)
	if err != nil {
		return nil, fmt.Errorf("captureindex: %w", err)
	}

	only := map[string]bool{}
	for _, want := range opts.IDs {
		only[want] = true
	}
	companions := ix.companions(ctx)
	state := loadState(id)
	// Oldest first, so a backfill indexes in capture order.
	for i := len(docs) - 1; i >= 0; i-- {
		d := docs[i]
		if d.CaptureDir == "" || (len(only) > 0 && !only[d.ID]) {
			continue
		}
		forced := opts.Force && only[d.ID]
		primary := forced || NeedsIndex(d, opts.RetryFailed)
		var extra []string
		if companions && (d.IndexError == "" || opts.RetryFailed || forced) {
			if forced {
				delete(state, filepath.Base(d.CaptureDir))
			}
			extra = companionsDue(d, state)
		}
		if !primary && len(extra) == 0 {
			if d.Indexed && !d.Stale {
				report.UpToDate++
			} else {
				report.Waiting++
			}
			continue
		}
		if ctx.Err() != nil {
			saveState(id, state)
			return report, ctx.Err()
		}
		out := ix.indexOne(ctx, db, id, d, primary, extra, state)
		if out.Indexed {
			report.Indexed++
		} else {
			report.Failed++
		}
		report.Outcomes = append(report.Outcomes, out)
	}
	if len(report.Outcomes) > 0 {
		saveState(id, state)
		touchStamp(id)
	}
	return report, nil
}

// touchStamp tells the app's inbox watcher that index statuses changed
// (capturedocs.IndexStampFile). Best effort: a missed refresh only delays
// the Documents list showing "Indexed".
func touchStamp(profileID string) {
	path := filepath.Join(profiledir.MonomindDir(nil, profileID), capturedocs.IndexStampFile)
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			f.Close()
		}
	}
}

// indexOne ingests one row — its primary document when primary is set,
// and the companion documents in extra — and records the result on the
// row, with the primary file's (mtime, size) as the staleness baseline on
// success. The row is Indexed only when everything attempted succeeded;
// otherwise the first error is recorded.
func (ix *Indexer) indexOne(ctx context.Context, db *sql.DB, profileID string, d vault.DocumentEntry, primary bool, extra []string, state indexState) Outcome {
	out := Outcome{ID: d.ID, Title: d.Filename, Path: d.Path}
	// Stat before ingesting: a file rewritten while it is being read is
	// then stale against this baseline and indexed again next pass.
	fi, statErr := os.Stat(d.Path)
	switch {
	case statErr != nil:
		out.Error = fmt.Sprintf("capture file unreadable: %v", statErr)
	case primary:
		if err := ix.ingest()(ctx, profileID, d.Path); err != nil {
			out.Error = err.Error()
		}
	}
	if out.Error == "" {
		key := filepath.Base(d.CaptureDir)
		for _, name := range extra {
			path := filepath.Join(d.CaptureDir, name)
			sig := fileSig(path)
			if err := ix.ingest()(ctx, profileID, path); err != nil {
				out.Error = fmt.Sprintf("%s: %v", name, err)
				break
			}
			if state[key] == nil {
				state[key] = map[string]string{}
			}
			state[key][name] = sig
		}
	}
	out.Indexed = out.Error == ""
	var mtime, size int64
	if out.Indexed {
		mtime, size = fi.ModTime().UnixNano(), fi.Size()
	}
	// The row's own context, not the pass's: a pass cancelled mid-ingest
	// still records what that ingest did.
	if err := vault.SetDocumentIndexed(context.WithoutCancel(ctx), db, profileID, d.ID, out.Indexed, out.Error, mtime, size); err != nil && out.Error == "" {
		out.Error = fmt.Sprintf("indexed, but the result could not be recorded: %v", err)
	}
	return out
}

// indexState remembers which companion documents were ingested, as
// envelope directory name -> artifact -> fileSig. The row's own staleness
// baseline covers only its primary file, and a summary lands minutes after
// its capture, so this is how a pass knows the summary is new. It lives
// beside the inbox (never inside an envelope, where it would read as an
// artifact) and is only read and written under the profile's lock.
type indexState map[string]map[string]string

const stateFile = ".capture-index.json"

func statePath(profileID string) string {
	return filepath.Join(profiledir.MonomindDir(nil, profileID), stateFile)
}

// loadState reads the state; missing or unreadable is empty, which at
// worst ingests a companion again (a no-op on monomind's side).
func loadState(profileID string) indexState {
	st := indexState{}
	if raw, err := os.ReadFile(statePath(profileID)); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

// saveState writes the state, dropping envelopes that left the inbox.
func saveState(profileID string, st indexState) {
	inbox, err := capture.ProfileInbox(profileID)
	if err != nil {
		return
	}
	for dir := range st {
		if _, err := os.Stat(filepath.Join(inbox, dir)); err != nil {
			delete(st, dir)
		}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	path := statePath(profileID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// fileSig identifies a version of a file cheaply: "" when it is missing
// or empty.
func fileSig(path string) string {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return ""
	}
	return strconv.FormatInt(fi.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(fi.Size(), 10)
}

// Status is how far a profile's captures are from searchable, for the Ask
// panel's "nothing yet" message.
type Status struct {
	Captures int `json:"captures"`
	Indexed  int `json:"indexed"`
	// Pending captures have not been indexed yet and have no error: a
	// pass is on its way (or the bridge that runs them is not running).
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
	// LastError is the newest failed capture's error.
	LastError string `json:"last_error,omitempty"`
}

// StatusOf syncs profileID's inbox into its rows and counts them.
func StatusOf(ctx context.Context, db *sql.DB, profileID string) (*Status, error) {
	id := strings.TrimSpace(profileID)
	if !profiledir.ValidProfileID(id) {
		return nil, fmt.Errorf("captureindex: unusable profile id %q", profileID)
	}
	capturedocs.Sync(ctx, db, id)
	docs, err := vault.ListDocuments(ctx, db, id)
	if err != nil {
		return nil, fmt.Errorf("captureindex: %w", err)
	}
	st := &Status{}
	for _, d := range docs { // newest first
		if d.CaptureDir == "" {
			continue
		}
		st.Captures++
		switch {
		case d.Indexed:
			st.Indexed++
		case d.IndexError != "":
			st.Failed++
			if st.LastError == "" {
				st.LastError = d.IndexError
			}
		default:
			st.Pending++
		}
	}
	return st, nil
}

// lockPath is the per-profile lock file. It lives in the profile's default
// monomind home beside the inbox — never inside it, where it would look
// like a capture — and is never deleted (see capturetask's lock for why).
func lockPath(profileID string) string {
	return filepath.Join(profiledir.MonomindDir(nil, profileID), ".capture-index.lock")
}

func lockProfile(profileID string) (func(), error) {
	path := lockPath(profileID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("captureindex: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("captureindex: lock: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("captureindex: lock: %w", err)
	}
	return func() {
		_ = unlockFile(f)
		_ = f.Close()
	}, nil
}
