package recording

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// writeRecording lands one finished recording in inbox via the ingest.
func writeRecording(t *testing.T, inbox, recID string, started time.Time, events int) string {
	t.Helper()
	in := &Ingest{Writer: &capture.Writer{Inbox: inbox}}
	f := Frame{Op: OpStart, RecordingID: recID, URL: "https://example.com/" + recID, Title: recID, StartedAt: started.UnixMilli()}
	if _, err := in.Handle(&f); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= events; i++ {
		e := ev("e"+string(rune('0'+i)), events+1-i, EvClick) // reverse seq order
		if _, err := in.Handle(&Frame{Op: OpEvent, RecordingID: recID, Event: e}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := in.Handle(&Frame{Op: OpStop, RecordingID: recID, Reason: "user"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := in.Finalize(out.Stopped)
	if err != nil {
		t.Fatal(err)
	}
	return res.Path
}

func TestListFindLoadDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	inbox := filepath.Join(t.TempDir(), "inbox")
	SetInboxes(inbox)
	t.Cleanup(func() { SetInboxes() })

	base := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	older := writeRecording(t, inbox, "rec-a", base, 2)
	newer := writeRecording(t, inbox, "rec-b", base.Add(time.Hour), 3)
	// A plain capture in the same inbox is not a recording.
	if _, err := (&capture.Writer{Inbox: inbox}).Write(&capture.Envelope{Meta: capture.Meta{URL: "https://example.com/page"},
		Artifacts: map[string]capture.Artifact{"readable.md": capture.Inline([]byte("x"))}}); err != nil {
		t.Fatal(err)
	}

	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Dir != newer || list[1].Dir != older {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Events != 3 || !list[0].Complete || list[0].StopReason != "user" || list[0].StartedAt != "2026-09-25T09:00:00Z" {
		t.Fatalf("summary = %+v", list[0])
	}

	for _, id := range []string{filepath.Base(newer), "rec-b", filepath.Base(newer)[:25]} {
		dir, err := Find(id)
		if err != nil || dir != newer {
			t.Fatalf("Find(%q) = %q, %v", id, dir, err)
		}
	}
	if _, err := Find("2026-09-25T0"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix: %v", err)
	}
	for _, bad := range []string{"../etc", "a/b", "-rf", "nope"} {
		if _, err := Find(bad); err == nil {
			t.Fatalf("Find(%q) succeeded", bad)
		}
	}

	sum, events, err := Load(newer)
	if err != nil || sum.ID != filepath.Base(newer) || len(events) != 3 {
		t.Fatalf("Load = %+v, %d, %v", sum, len(events), err)
	}
	for i, e := range events {
		if e.Seq != i+1 {
			t.Fatalf("events not in seq order: %+v", events)
		}
	}

	if err := SetAutomation(newer, "acme"); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(); list[0].Automation != "acme" {
		t.Fatalf("automation link not listed: %+v", list[0])
	}

	if err := Delete("rec-a"); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(); len(list) != 1 {
		t.Fatalf("after delete: %+v", list)
	}
	if entries, _ := capture.List(inbox); len(entries) != 2 {
		t.Fatalf("delete touched the non-recording capture: %d left", len(entries))
	}
}

func TestProfileScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(capture.InboxEnv, filepath.Join(home, "default-inbox"))
	work, err := capture.ProfileInbox("work")
	if err != nil {
		t.Fatal(err)
	}
	writeRecording(t, work, "rec-w", time.Now(), 1)
	writeRecording(t, capture.DefaultInbox(), "rec-d", time.Now().Add(-time.Hour), 1)

	list, err := List()
	if err != nil || len(list) != 2 || list[0].Profile != "work" || list[1].Profile != "" {
		t.Fatalf("all-inbox list = %+v, %v", list, err)
	}
	if err := SetProfile("work"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetProfile("") })
	if list, _ := List(); len(list) != 1 || list[0].Profile != "work" {
		t.Fatalf("profile list = %+v", list)
	}
	if err := SetProfile("../x"); err == nil {
		t.Fatal("bad profile accepted")
	}
}

func TestResolveDraftDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, _ := DraftsDir()
	good := filepath.Join(root, "rec-1")
	if err := os.MkdirAll(good, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "elsewhere")
	_ = os.MkdirAll(outside, 0o700)
	_ = os.Symlink(outside, filepath.Join(root, "sneaky"))

	for _, ref := range []string{good, "rec-1", good + "/"} {
		if dir, err := ResolveDraftDir(ref); err != nil || filepath.Base(dir) != "rec-1" {
			t.Errorf("ResolveDraftDir(%q) = %q, %v", ref, dir, err)
		}
	}
	for _, ref := range []string{"", "-x", root, outside, filepath.Join(root, "..", "elsewhere"),
		"../elsewhere", "sneaky", filepath.Join(root, "sneaky"), filepath.Join(root, "missing"), "a/b"} {
		if dir, err := ResolveDraftDir(ref); err == nil {
			t.Errorf("ResolveDraftDir(%q) accepted: %s", ref, dir)
		}
	}
}
