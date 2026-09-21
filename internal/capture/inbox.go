package capture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InboxEnv overrides the inbox location outright; HomeEnv overrides only
// the monomind home it sits under. Both exist because the inbox is a
// contract shared with the monomind side of the browser track — the tests
// on either side need to point it somewhere disposable, and a user who
// moved ~/.monomind should not have captures land somewhere else.
const (
	InboxEnv = "MONOMIND_INBOX"
	HomeEnv  = "MONOMIND_HOME"
)

// DefaultInbox is where captures land: ~/.monomind/inbox, unless
// overridden. It never fails — a home directory that cannot be resolved
// falls back to a relative path rather than stopping a capture.
func DefaultInbox() string {
	if dir := strings.TrimSpace(os.Getenv(InboxEnv)); dir != "" {
		return dir
	}
	if home := strings.TrimSpace(os.Getenv(HomeEnv)); home != "" {
		return filepath.Join(home, "inbox")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".monomind", "inbox")
	}
	return filepath.Join(home, ".monomind", "inbox")
}

// Entry is one envelope directory as `capture list` reports it.
type Entry struct {
	Path       string   `json:"path"`
	URL        string   `json:"url"`
	Title      string   `json:"title"`
	CapturedAt string   `json:"capturedAt"`
	Bytes      int64    `json:"bytes"`
	Artifacts  []string `json:"artifacts"`
}

// List returns the envelopes in inbox, newest first. Directories still
// being staged (dot-prefixed) and directories without a meta.json are
// skipped: both are captures that have not landed, and neither should be
// ingested. A missing inbox is not an error — it just has nothing in it.
func List(inbox string) ([]Entry, error) {
	if strings.TrimSpace(inbox) == "" {
		inbox = DefaultInbox()
	}
	dirents, err := os.ReadDir(inbox)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read inbox %s: %w", inbox, err)
	}

	entries := make([]Entry, 0, len(dirents))
	for _, d := range dirents {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		entry, ok := readEntry(filepath.Join(inbox, d.Name()))
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CapturedAt != entries[j].CapturedAt {
			return entries[i].CapturedAt > entries[j].CapturedAt
		}
		return entries[i].Path > entries[j].Path
	})
	return entries, nil
}

// readEntry reads one envelope directory, reporting false for anything
// that is not a landed capture.
func readEntry(dir string) (Entry, bool) {
	blob, err := os.ReadFile(filepath.Join(dir, MetaFile))
	if err != nil {
		return Entry{}, false
	}
	var meta Meta
	if err := json.Unmarshal(blob, &meta); err != nil {
		return Entry{}, false
	}
	entry := Entry{
		Path:       dir,
		URL:        meta.DedupeURL(),
		Title:      meta.Title,
		CapturedAt: meta.CapturedAt,
		Bytes:      int64(len(blob)),
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return Entry{}, false
	}
	for _, f := range files {
		if f.IsDir() || f.Name() == MetaFile {
			continue
		}
		entry.Artifacts = append(entry.Artifacts, f.Name())
		if info, err := f.Info(); err == nil {
			entry.Bytes += info.Size()
		}
	}
	sort.Strings(entry.Artifacts)
	return entry, true
}
