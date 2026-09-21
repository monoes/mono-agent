package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// runCaptureCmd executes a `capture` subcommand with stdout captured.
func runCaptureCmd(t *testing.T, cfg *globalConfig, args ...string) (string, error) {
	t.Helper()
	if cfg == nil {
		cfg = &globalConfig{}
	}
	cmd := newCaptureCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	return out.String(), err
}

// seedInbox writes one landed capture and returns the inbox directory.
func seedInbox(t *testing.T, captures ...capture.Meta) string {
	t.Helper()
	inbox := filepath.Join(t.TempDir(), "inbox")
	for _, meta := range captures {
		w := &capture.Writer{Inbox: inbox, Now: func() time.Time { return time.Now() }}
		if _, err := w.Write(&capture.Envelope{
			Meta: meta,
			Artifacts: map[string]capture.Artifact{
				capture.ArtifactMHTML:    capture.Inline([]byte("archive")),
				capture.ArtifactReadable: capture.Inline([]byte("# body")),
			},
		}); err != nil {
			t.Fatalf("seed %s: %v", meta.URL, err)
		}
	}
	return inbox
}

func TestCaptureListText(t *testing.T) {
	inbox := seedInbox(t,
		capture.Meta{URL: "https://example.com/old", Title: "Older", CapturedAt: "2026-09-19T08:00:00Z"},
		capture.Meta{URL: "https://example.com/new", Title: "Newest", CapturedAt: "2026-09-21T08:00:00Z"},
	)
	out, err := runCaptureCmd(t, nil, "list", "--out", inbox)
	if err != nil {
		t.Fatalf("capture list: %v", err)
	}
	if !strings.Contains(out, "Newest") || !strings.Contains(out, "Older") {
		t.Fatalf("output missing captures:\n%s", out)
	}
	if strings.Index(out, "Newest") > strings.Index(out, "Older") {
		t.Fatalf("captures are not newest-first:\n%s", out)
	}
	if !strings.Contains(out, "https://example.com/new") {
		t.Fatalf("output missing url:\n%s", out)
	}
}

func TestCaptureListJSON(t *testing.T) {
	inbox := seedInbox(t, capture.Meta{
		URL:        "https://example.com/post",
		Title:      "A Post",
		CapturedAt: "2026-09-21T08:00:00Z",
	})
	out, err := runCaptureCmd(t, &globalConfig{JSONOutput: true}, "list", "--out", inbox)
	if err != nil {
		t.Fatalf("capture list --json: %v", err)
	}
	var entries []capture.Entry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Title != "A Post" || entries[0].URL != "https://example.com/post" {
		t.Fatalf("entry = %+v", entries[0])
	}
	if len(entries[0].Artifacts) != 2 || entries[0].Bytes <= 0 {
		t.Fatalf("entry artifacts/size = %+v", entries[0])
	}
}

func TestCaptureListEmptyInbox(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "nothing-here")
	out, err := runCaptureCmd(t, nil, "list", "--out", empty)
	if err != nil {
		t.Fatalf("capture list: %v", err)
	}
	if !strings.Contains(out, "No captures") {
		t.Fatalf("output = %q", out)
	}

	jsonOut, err := runCaptureCmd(t, &globalConfig{JSONOutput: true}, "list", "--out", empty)
	if err != nil {
		t.Fatalf("capture list --json: %v", err)
	}
	if strings.TrimSpace(jsonOut) != "[]" {
		t.Fatalf("--json on an empty inbox = %q, want [] so a caller can parse it", jsonOut)
	}
}

// TestCaptureListSkipsHalfWrittenCaptures: the CLI must agree with the
// watcher about what has actually landed.
func TestCaptureListSkipsHalfWrittenCaptures(t *testing.T) {
	inbox := seedInbox(t, capture.Meta{URL: "https://example.com/real", Title: "Real", CapturedAt: "2026-09-21T08:00:00Z"})
	staging := filepath.Join(inbox, ".tmp-capture-inflight")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, capture.MetaFile),
		[]byte(`{"url":"https://example.com/partial","title":"Partial"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := runCaptureCmd(t, nil, "list", "--out", inbox)
	if err != nil {
		t.Fatalf("capture list: %v", err)
	}
	if strings.Contains(out, "Partial") {
		t.Fatalf("listed a capture that never landed:\n%s", out)
	}
}

// TestCapturePageRejectsBadFlagsBeforeOpeningChrome: validation must fail
// with the invalid-input exit code without ever reaching for the browser.
//
// The exit code alone does not say that. A run that got as far as Chrome
// and failed there is also an error, so each case pins the message to the
// flag it is about, and a message that talks about a browser, a daemon or
// a connection fails the test — those are the words of a command that went
// looking. The inbox is pointed somewhere disposable and checked too: a
// validation that ran too late could already have written a capture.
func TestCapturePageRejectsBadFlagsBeforeOpeningChrome(t *testing.T) {
	cases := []struct {
		args []string
		says string
	}{
		{[]string{"page", "--formats", "mhtml,wat"}, "--formats"},
		{[]string{"page", "--formats", ","}, "--formats"},
		{[]string{"page", "--tab", "-3"}, "--tab"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			home := t.TempDir()
			inbox := filepath.Join(home, "inbox")
			t.Setenv(capture.InboxEnv, inbox)
			t.Setenv(capture.HomeEnv, home)

			_, err := runCaptureCmd(t, nil, tc.args...)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if got := exitCodeFor(err); got != 3 {
				t.Fatalf("exit code = %d (%v), want 3", got, err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %v, want it to name %s", err, tc.says)
			}
			// ("chrome" is not in this list: --tab's own refusal names the
			// Chrome tab id, which is the flag talking, not a browser.)
			for _, word := range []string{"browser", "launch", "connect", "daemon", "websocket"} {
				if strings.Contains(strings.ToLower(err.Error()), word) {
					t.Errorf("the command reached for the browser before validating: %v", err)
				}
			}
			if entries, _ := capture.List(inbox); len(entries) != 0 {
				t.Errorf("a refused capture wrote %d envelopes", len(entries))
			}
			if left, _ := os.ReadDir(home); len(left) != 0 {
				t.Errorf("a refused capture wrote %v", left)
			}
		})
	}
}

func TestNormalizeCaptureFormats(t *testing.T) {
	got, err := normalizeCaptureFormats([]string{"MHTML", " readable ", "mhtml"})
	if err != nil {
		t.Fatalf("normalizeCaptureFormats: %v", err)
	}
	if len(got) != 2 || got[0] != "mhtml" || got[1] != "readable" {
		t.Fatalf("got = %v", got)
	}
	if got, err := normalizeCaptureFormats(nil); err != nil || got != nil {
		t.Fatalf("nil formats = %v, %v; want nil, nil so the default set applies", got, err)
	}
	if _, err := normalizeCaptureFormats([]string{"har"}); err == nil {
		t.Fatal("accepted an unknown format")
	}
}

func TestCaptureHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:             "0 B",
		512:           "512 B",
		2048:          "2.0 KB",
		5 << 20:       "5.0 MB",
		3 << 30:       "3.0 GB",
		1536:          "1.5 KB",
		1024*1024 - 1: "1024.0 KB",
		1 << 40:       "1.0 TB",
		1 << 50:       "1.0 PB",
		1 << 60:       "1.0 EB",
		math.MaxInt64: "8.0 EB",
	}
	for in, want := range cases {
		if got := captureHumanBytes(in); got != want {
			t.Errorf("captureHumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateCaptureCell(t *testing.T) {
	if got := truncateCaptureCell("short", 40); got != "short" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("a", 50)
	got := truncateCaptureCell(long, 10)
	if len([]rune(got)) != 10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %q", got)
	}
}

// TestCaptureCommandIsRegistered guards the wiring: a command that exists
// but is never added to the root tree is invisible to every caller.
func TestCaptureCommandIsRegistered(t *testing.T) {
	root := newRootCmd()
	for _, path := range [][]string{{"capture"}, {"capture", "page"}, {"capture", "list"}} {
		sub, _, err := root.Find(path)
		if err != nil || sub == nil || sub.Name() != path[len(path)-1] {
			t.Fatalf("root.Find(%v) = %v, %v", path, sub, err)
		}
	}
}
