package capturedocs

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// DefaultPollInterval is how often Watcher looks at the inbox. Listing one
// flat directory is cheap, and the latency it hides is a person clicking
// "save page" and then looking at the app.
const DefaultPollInterval = 3 * time.Second

// Watcher polls one inbox and calls onChange when the set of landed
// captures in it changes (one added, removed or rewritten). It only
// notices; it never touches the database. The GUI answers a change by
// re-fetching the document list, and that list — `profile documents list`
// in the CLI — is what syncs captures into the vault.
//
// The first poll primes the snapshot silently: whoever started the watcher
// is about to load the list anyway.
type Watcher struct {
	inbox    string
	interval time.Duration
	onChange func()

	mu   sync.Mutex
	last string

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewWatcher creates a Watcher for inbox; interval <= 0 means
// DefaultPollInterval. Call Start to begin polling.
func NewWatcher(inbox string, interval time.Duration, onChange func()) *Watcher {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	return &Watcher{inbox: inbox, interval: interval, onChange: onChange, stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

// Start primes the snapshot and polls in the background until Stop.
func (w *Watcher) Start() {
	w.poll(false)
	go func() {
		defer close(w.doneCh)
		t := time.NewTicker(w.interval)
		defer t.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-t.C:
				w.poll(true)
			}
		}
	}()
}

// Stop ends polling and waits for the goroutine to exit. Safe to call more
// than once.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	<-w.doneCh
}

func (w *Watcher) poll(emit bool) {
	sig, ok := InboxSignature(w.inbox)
	if !ok {
		return // transient read error: try again next tick
	}
	w.mu.Lock()
	changed := sig != w.last
	w.last = sig
	w.mu.Unlock()
	if changed && emit && w.onChange != nil {
		w.onChange()
	}
}

// InboxSignature summarises the landed captures in inbox — each envelope
// directory's name and its meta.json's mtime and size — so two calls
// return the same string exactly when the listing would be the same.
// Staging directories (dot-prefixed) and directories without a meta.json
// are ignored, as capture.List ignores them. A missing inbox is empty, not
// an error; ok is false only for an inbox that exists but cannot be read.
func InboxSignature(inbox string) (sig string, ok bool) {
	dirents, err := os.ReadDir(inbox)
	if err != nil {
		if os.IsNotExist(err) {
			return "", true
		}
		return "", false
	}
	parts := make([]string, 0, len(dirents))
	for _, d := range dirents {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		fi, err := os.Stat(filepath.Join(inbox, d.Name(), capture.MetaFile))
		if err != nil {
			continue
		}
		parts = append(parts, d.Name()+"\x00"+fi.ModTime().UTC().Format(time.RFC3339Nano)+"\x00"+strconv.FormatInt(fi.Size(), 10))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n"), true
}
