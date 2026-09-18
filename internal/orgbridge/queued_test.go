package orgbridge

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeInbox(t *testing.T, root, org, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, ".monomind", "orgs", org)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadQueued_NoInboxIsEmpty(t *testing.T) {
	msgs, skipped, err := ReadQueued(t.TempDir(), "growth")
	if err != nil || len(msgs) != 0 || skipped != 0 {
		t.Fatalf("got %v, %d, %v; want empty", msgs, skipped, err)
	}
}

func TestReadQueued_RejectsUnsafeOrgName(t *testing.T) {
	if _, _, err := ReadQueued(t.TempDir(), "../etc"); err == nil {
		t.Fatal("expected an error for a path-traversal org name")
	}
}

// C-35: the list mirrors monomind's peekInbox — an interrupted drain
// (.draining) first, then the pending file — skips corrupt lines, and never
// touches either file.
func TestReadQueued_ReadsBothFilesReadOnly(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC).UnixMilli()
	pending := `{"fromQualified":"hq:ceo","toRole":"lead","subject":"Q3","body":"[trace chn_abc hop=2]\ngo","ts":` +
		itoa(ts) + `,"messageId":"msg-1"}
not json
{"fromQualified":"workflow:ex1","toRole":"publisher-bot","subject":"post","body":"hi","ts":` + itoa(ts+1000) + `,"endpoint":true}
`
	draining := `{"fromQualified":"human","toRole":"lead","subject":"old","body":"earlier","ts":` + itoa(ts-1000) + `}` + "\n"
	pPath := writeInbox(t, root, "growth", "inbox.jsonl", pending)
	dPath := writeInbox(t, root, "growth", "inbox.jsonl.draining", draining)
	pBefore, _ := os.Stat(pPath)
	dBefore, _ := os.Stat(dPath)

	msgs, skipped, err := ReadQueued(root, "growth")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1", skipped)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages: %+v", len(msgs), msgs)
	}
	if msgs[0].Subject != "old" || !msgs[0].Draining {
		t.Fatalf("first message should be the interrupted drain's: %+v", msgs[0])
	}
	m := msgs[1]
	if m.From != "hq:ceo" || m.To != "lead" || m.MessageID != "msg-1" || m.Draining {
		t.Fatalf("unexpected message: %+v", m)
	}
	if m.Body != "go" || m.Trace == nil || m.Trace.ChainID != "chn_abc" || m.Trace.Hop != 2 {
		t.Fatalf("trace not split from body: %+v", m)
	}
	if m.QueuedAt != "2026-09-18T10:00:00Z" {
		t.Fatalf("queued_at = %q", m.QueuedAt)
	}
	if !msgs[2].Endpoint || msgs[2].Trace != nil {
		t.Fatalf("endpoint entry: %+v", msgs[2])
	}

	for path, before := range map[string]os.FileInfo{pPath: pBefore, dPath: dBefore} {
		after, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s disappeared: %v", path, err)
		}
		if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
			t.Fatalf("%s was modified", path)
		}
	}
	if b, _ := os.ReadFile(pPath); !bytes.Equal(b, []byte(pending)) {
		t.Fatal("inbox.jsonl content changed")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
