package capturedocs_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capturedocs"
)

func TestInboxSignature(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	empty, ok := capturedocs.InboxSignature(inbox)
	if !ok || empty != "" {
		t.Fatalf("missing inbox = %q, %v; want empty, ok", empty, ok)
	}

	// A staging directory and a directory without meta.json do not count.
	if err := os.MkdirAll(filepath.Join(inbox, ".tmp-capture-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(inbox, "no-meta"), 0o700); err != nil {
		t.Fatal(err)
	}
	if sig, _ := capturedocs.InboxSignature(inbox); sig != "" {
		t.Fatalf("unlanded captures changed the signature: %q", sig)
	}

	writeCapture(t, inbox, "a", meta("A", "https://a.test", "2026-09-22T10:00:00Z"), "readable.md")
	one, _ := capturedocs.InboxSignature(inbox)
	if one == "" {
		t.Fatal("a landed capture must change the signature")
	}
	again, _ := capturedocs.InboxSignature(inbox)
	if again != one {
		t.Fatal("signature is not stable")
	}
}

func TestWatcher_FiresOnANewCapture(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	writeCapture(t, inbox, "a", meta("A", "https://a.test", "2026-09-22T10:00:00Z"), "readable.md")

	fired := make(chan struct{}, 8)
	w := capturedocs.NewWatcher(inbox, 20*time.Millisecond, func() { fired <- struct{}{} })
	w.Start()
	defer w.Stop()

	// Priming is silent: nothing changed since Start.
	select {
	case <-fired:
		t.Fatal("watcher fired without a change")
	case <-time.After(100 * time.Millisecond):
	}

	writeCapture(t, inbox, "b", meta("B", "https://b.test", "2026-09-22T11:00:00Z"), "page.mhtml")
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not notice a new capture")
	}
}

func TestInboxSignatureSeesIndexStamp(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	before, _ := capturedocs.InboxSignature(inbox)
	if err := os.WriteFile(filepath.Join(root, capturedocs.IndexStampFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	after, _ := capturedocs.InboxSignature(inbox)
	if before == after {
		t.Fatal("an indexing pass must change the signature, so the Documents list refreshes")
	}
}
