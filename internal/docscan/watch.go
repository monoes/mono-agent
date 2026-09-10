package docscan

import (
	"sync"
	"time"
)

// DefaultPollInterval is longer than orgdesign's 1.5s because this walks a
// whole, potentially deep profile tree every tick rather than ReadDir'ing
// one small flat directory -- and because the latency this hides is a
// human dropping a file in Finder (imperceptible at a few seconds), not an
// in-app AI/canvas edit loop where sub-second feedback matters.
const DefaultPollInterval = 5 * time.Second

type fileState struct {
	modTime int64
	size    int64
}

// Watcher polls root recursively and invokes onChange with the FULL
// current matching-file snapshot whenever that snapshot differs from the
// previous tick (add/modify/remove of any tracked file) -- never a
// per-file callback. Unlike orgdesign.Watcher's (modTime,size)-then-sha256
// two-tier diff, this uses (modTime, size) only: nothing in this app ever
// rewrites a discovered or uploaded file's bytes after the fact, so there
// is no self-write to distinguish from a genuine external edit -- the one
// thing orgdesign needs sha256 for. Hashing potentially large PDFs/DOCX on
// every tick for that non-existent purpose would be pure waste.
//
// Unlike orgdesign's Start (which primes its snapshot silently and only
// emits for changes after that), this Watcher's first scan DOES emit:
// nothing else already knows about pre-existing on-disk documents, so the
// first pass must itself register everything already on disk.
type Watcher struct {
	root     string
	interval time.Duration
	onChange func([]FileInfo)

	mu      sync.Mutex
	files   map[string]fileState // keyed by absolute path
	scanned bool                 // false until the first scan completes -- see scan's doc comment

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewWatcher creates a Watcher for root. Call Start to begin polling. An
// interval <= 0 uses DefaultPollInterval.
func NewWatcher(root string, interval time.Duration, onChange func([]FileInfo)) *Watcher {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return &Watcher{
		root:     root,
		interval: interval,
		onChange: onChange,
		files:    make(map[string]fileState),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start scans immediately (emitting if anything is found) and then polls in
// a background goroutine. Safe to call once; call Stop to end it.
func (w *Watcher) Start() {
	w.scan(true)
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

// scan walks root once, diffs against the last known snapshot, and invokes
// onChange with the full current snapshot if it differs (add, modify, or
// remove of any tracked file) and emit is true. The very first scan always
// counts as a change (even an empty root), since no other mechanism
// already knows what's on disk before this watcher runs for the first
// time.
func (w *Watcher) scan(emit bool) {
	found, err := Scan(w.root)
	if err != nil {
		return // transient read error -- try again next tick
	}

	w.mu.Lock()
	cur := make(map[string]fileState, len(found))
	for _, f := range found {
		cur[f.Path] = fileState{modTime: f.ModTime, size: f.SizeBytes}
	}
	changed := !w.scanned || len(cur) != len(w.files)
	if !changed {
		for path, state := range cur {
			if prev, ok := w.files[path]; !ok || prev != state {
				changed = true
				break
			}
		}
	}
	w.files = cur
	w.scanned = true
	w.mu.Unlock()

	if changed && emit {
		w.onChange(found)
	}
}
