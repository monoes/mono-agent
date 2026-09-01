package orgdesign

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// pollInterval is how often Watcher scans the orgs directory. Deliberately
// a poller rather than fsnotify (which is not a dependency anywhere in this
// repo): profile roots can live on external or synced volumes where
// fsnotify is unreliable-to-nonfunctional (see internal/profiledir's own
// doc comment and App.MoveProfileFolder, which lets a user point a profile
// at exactly such a location), a poller needs no debounce logic of its own
// (a single edit's create/write/rename sequence collapses into one
// observation by construction), and the orgs directory is tiny — a
// ReadDir+Stat pass every interval is effectively free. The cost is up to
// one interval of latency for externally-originated changes, which is
// imperceptible against an AI turn that took tens of seconds to produce the
// edit in the first place.
const pollInterval = 1500 * time.Millisecond

// Change describes one detected difference in the watched orgs directory.
type Change struct {
	Name    string // org name (file stem)
	Path    string
	Deleted bool
	Doc     *Doc     // nil when Deleted, or when the file failed to parse
	Errors  []string // parse or validation errors; non-empty means the config on disk is currently broken
}

type fileState struct {
	modTime int64 // UnixNano
	size    int64
	sha     string // populated lazily, only recomputed when modTime/size change
}

// Watcher polls a single orgs directory non-recursively for changes to real
// org CONFIG files (IsOrgConfigFile) and invokes onChange for each one.
// Non-recursive is deliberate: per-org run state lives in
// <orgsDir>/<name>/ subdirectories (bus.jsonl, stop/pause/reload sentinels,
// checkpoints) written constantly by a running daemon — none of that is a
// design change, and descending into it would make the watcher fire
// continuously on a running org.
type Watcher struct {
	dir      string
	interval time.Duration
	onChange func(Change)

	mu    sync.Mutex
	files map[string]fileState // keyed by base filename
	// selfWrites records a sha this process itself just wrote for a given
	// org name, so the next scan that observes exactly that content
	// suppresses the callback once rather than re-announcing the app's own
	// save as if it were an external change. Content-hash matching (not a
	// time window) is deliberate: a time-based suppression would also
	// swallow a genuine external edit that happens to land within the
	// window.
	selfWrites map[string]string

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewWatcher creates a Watcher for orgsDir. Call Start to begin polling.
func NewWatcher(orgsDir string, interval time.Duration, onChange func(Change)) *Watcher {
	if interval <= 0 {
		interval = pollInterval
	}
	return &Watcher{
		dir:        orgsDir,
		interval:   interval,
		onChange:   onChange,
		files:      make(map[string]fileState),
		selfWrites: make(map[string]string),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
}

// MarkSelfWrite registers the sha256 (hex) this process itself just wrote
// for org name, so the next scan observing that exact content suppresses
// the change callback once. Callers should call this BEFORE emitting their
// own live-update event for the same write, so there is no window where
// both fire.
func (w *Watcher) MarkSelfWrite(name, sha string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.selfWrites[name] = sha
}

// Start begins polling in a background goroutine. Safe to call once; call
// Stop to end it.
func (w *Watcher) Start() {
	// Prime the initial snapshot without emitting changes for files that
	// already existed before the watcher started — only report changes
	// going forward.
	w.scan(false)
	go func() {
		defer close(w.doneCh)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.scan(true)
			}
		}
	}()
}

// Stop ends polling and waits for the background goroutine to exit.
func (w *Watcher) Stop() {
	close(w.stopCh)
	<-w.doneCh
}

// scan reads the directory once, diffs against the last known snapshot, and
// invokes onChange for every file whose (modTime, size) changed, was
// added, or disappeared. When emit is false (the initial priming scan),
// the snapshot is built but no callbacks fire.
func (w *Watcher) scan(emit bool) {
	entries, err := os.ReadDir(w.dir)
	if os.IsNotExist(err) {
		entries = nil
	} else if err != nil {
		return // transient read error (e.g. a synced volume momentarily unmounted) — try again next tick
	}

	w.mu.Lock()
	seen := make(map[string]bool, len(entries))
	var changes []Change
	for _, e := range entries {
		if e.IsDir() || !IsOrgConfigFile(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		seen[e.Name()] = true
		prev, existed := w.files[e.Name()]
		cur := fileState{modTime: info.ModTime().UnixNano(), size: info.Size()}
		if existed && prev.modTime == cur.modTime && prev.size == cur.size {
			continue // unchanged
		}

		path := filepath.Join(w.dir, e.Name())
		content, readErr := os.ReadFile(path)
		name := e.Name()[:len(e.Name())-len(".json")]

		if readErr != nil {
			w.files[e.Name()] = cur
			if emit {
				changes = append(changes, Change{Name: name, Path: path, Errors: []string{readErr.Error()}})
			}
			continue
		}

		sum := sha256.Sum256(content)
		sha := hex.EncodeToString(sum[:])
		cur.sha = sha
		w.files[e.Name()] = cur

		if selfSha, ok := w.selfWrites[name]; ok && selfSha == sha {
			delete(w.selfWrites, name)
			continue // our own write, already announced by the writer — suppress
		}

		if !emit {
			continue
		}

		doc, parseErr := LoadPath(path)
		if parseErr != nil {
			changes = append(changes, Change{Name: name, Path: path, Errors: []string{parseErr.Error()}})
			continue
		}
		var errs []string
		if valErr := Validate(doc); valErr != nil {
			if ve, ok := valErr.(*ValidationError); ok {
				errs = ve.Errors
			} else {
				errs = []string{valErr.Error()}
			}
		}
		changes = append(changes, Change{Name: name, Path: path, Doc: doc, Errors: errs})
	}

	// Anything previously tracked but no longer present was deleted.
	for fname, prev := range w.files {
		_ = prev
		if !seen[fname] {
			delete(w.files, fname)
			if emit {
				name := fname[:len(fname)-len(".json")]
				changes = append(changes, Change{Name: name, Path: filepath.Join(w.dir, fname), Deleted: true})
			}
		}
	}
	w.mu.Unlock()

	for _, c := range changes {
		w.onChange(c)
	}
}
