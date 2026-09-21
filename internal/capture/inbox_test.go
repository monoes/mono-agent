package capture

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seed writes one envelope into inbox at the given capture time.
func seed(t *testing.T, inbox, url, title, at string) string {
	t.Helper()
	w := &Writer{Inbox: inbox, Now: func() time.Time { return time.Now() }}
	res, err := w.Write(&Envelope{
		Meta:      Meta{URL: url, Title: title, CapturedAt: at},
		Artifacts: map[string]Artifact{ArtifactMHTML: Inline([]byte("archive")), ArtifactReadable: Inline([]byte("# t"))},
	})
	if err != nil {
		t.Fatalf("seed %s: %v", url, err)
	}
	return res.Path
}

func TestListReturnsNewestFirst(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	seed(t, inbox, "https://example.com/old", "Old", "2026-09-19T08:00:00Z")
	newest := seed(t, inbox, "https://example.com/new", "New", "2026-09-21T08:00:00Z")
	seed(t, inbox, "https://example.com/mid", "Mid", "2026-09-20T08:00:00Z")

	entries, err := List(inbox)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[0].Path != newest {
		t.Fatalf("first entry = %s, want %s", entries[0].Path, newest)
	}
	if entries[0].Title != "New" || entries[0].URL != "https://example.com/new" {
		t.Fatalf("entry = %+v", entries[0])
	}
	if entries[0].Bytes <= 0 {
		t.Fatalf("entry bytes = %d", entries[0].Bytes)
	}
	if len(entries[0].Artifacts) != 2 {
		t.Fatalf("artifacts = %v", entries[0].Artifacts)
	}
}

// TestListSkipsHalfWrittenCaptures: a staging directory and a directory
// with no meta.json are both captures that never landed, and a watcher —
// or this listing — must not treat either as ingestible.
func TestListSkipsHalfWrittenCaptures(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	seed(t, inbox, "https://example.com/real", "Real", "2026-09-21T08:00:00Z")

	staging := filepath.Join(inbox, tmpPrefix+"123")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, MetaFile), []byte(`{"url":"https://example.com/partial"}`), 0o600); err != nil {
		t.Fatalf("write staging meta: %v", err)
	}
	noMeta := filepath.Join(inbox, "2026-09-21T09-00-00Z-example-com-nometa")
	if err := os.MkdirAll(noMeta, 0o700); err != nil {
		t.Fatalf("mkdir nometa: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	entries, err := List(inbox)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Title != "Real" {
		t.Fatalf("entries = %+v, want only the landed capture", entries)
	}
}

func TestListMissingInboxIsEmpty(t *testing.T) {
	entries, err := List(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %v", entries)
	}
}

func TestDefaultInboxHonoursOverrides(t *testing.T) {
	t.Setenv(HomeEnv, filepath.Join("/somewhere", "mm"))
	t.Setenv(InboxEnv, "")
	if got, want := DefaultInbox(), filepath.Join("/somewhere", "mm", "inbox"); got != want {
		t.Fatalf("DefaultInbox = %q, want %q", got, want)
	}
	t.Setenv(InboxEnv, "/elsewhere/box")
	if got := DefaultInbox(); got != "/elsewhere/box" {
		t.Fatalf("DefaultInbox = %q", got)
	}
}

func TestDefaultInboxIsUnderMonomindHome(t *testing.T) {
	t.Setenv(HomeEnv, "")
	t.Setenv(InboxEnv, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := DefaultInbox(), filepath.Join(home, ".monomind", "inbox"); got != want {
		t.Fatalf("DefaultInbox = %q, want %q", got, want)
	}
}
