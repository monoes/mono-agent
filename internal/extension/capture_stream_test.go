package extension

// The capture path at a size that actually exercises streaming: artifacts
// delivered in multi-megabyte frames, spooled to disk and reassembled. The
// fake-extension harness these use lives in capture_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// TestCapturePageStreamsALargeArtifact exercises the path at a size that
// actually matters: an archive delivered in multi-megabyte frames, which
// the assembler spools to disk rather than holding whole in memory.
func TestCapturePageStreamsALargeArtifact(t *testing.T) {
	srv, ext, inbox := startCaptureServer(t)

	const (
		chunks    = 3
		chunkSize = 2 << 20
	)
	// Distinct per-chunk filler, so a reassembly that reorders or drops a
	// chunk cannot pass by accident.
	bodies := make([]string, chunks)
	var want strings.Builder
	for i := range bodies {
		bodies[i] = strings.Repeat(string(rune('a'+i)), chunkSize)
		want.WriteString(bodies[i])
	}

	type outcome struct {
		res *capture.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := srv.CapturePage(CaptureRequest{Timeout: 30 * time.Second})
		done <- outcome{res, err}
	}()

	cmd := ext.nextCommand()
	for i, body := range bodies {
		ext.sendChunk(cmd.ID, capture.ArtifactMHTML, i, chunks, body)
	}
	ext.sendFinal(cmd.ID, sampleMeta(),
		chunkReceipt(capture.ArtifactMHTML, chunks),
		b64Artifact(capture.ArtifactReadable, "# A Post"),
	)

	got := <-done
	if got.err != nil {
		t.Fatalf("CapturePage: %v", got.err)
	}
	onDisk, err := os.ReadFile(filepath.Join(got.res.Path, capture.ArtifactMHTML))
	if err != nil {
		t.Fatalf("read page.mhtml: %v", err)
	}
	if len(onDisk) != chunks*chunkSize {
		t.Fatalf("page.mhtml is %d bytes, want %d", len(onDisk), chunks*chunkSize)
	}
	if string(onDisk) != want.String() {
		t.Fatal("page.mhtml was reassembled in the wrong order")
	}
	// Nothing may be left staged in the inbox once the capture has landed.
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leftover staging/spool directory %s", e.Name())
		}
	}
}
