package capture

// Tests for the spooling half: chunk bytes going to disk instead of memory,
// and the spool directory being cleaned up on every path out of a capture.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// countSpoolDirs reports how many capture spool directories exist under
// root, for asserting that nothing is left behind.
func countSpoolDirs(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read spool root: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), spoolPrefix) {
			n++
		}
	}
	return n
}

// TestChunksAreSpooledToDiskNotHeldInMemory is the memory guarantee: a
// streamed artifact's bytes live in the spool directory while the capture
// is in flight, not in the assembler. A 100MB capture must not cost 100MB
// of RAM per capture in flight.
func TestChunksAreSpooledToDiskNotHeldInMemory(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "spool")
	a := NewAssembler(Options{SpoolDir: spool})

	const body = "a fairly chunky artifact"
	for i, part := range []string{body[:8], body[8:16], body[16:]} {
		if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, i, 3, part)); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
	if countSpoolDirs(t, spool) != 1 {
		t.Fatalf("expected one spool directory under %s", spool)
	}
	onDisk := spoolBytes(t, spool)
	if onDisk != int64(len(body)) {
		t.Fatalf("spooled %d bytes, want %d — the chunks are not on disk", onDisk, len(body))
	}

	env, err := a.Accept("c1", finalEnvelope(chunkedEntry(ArtifactMHTML, 3)))
	if err != nil {
		t.Fatalf("Accept final: %v", err)
	}
	artifact := env.Artifacts[ArtifactMHTML]
	if !artifact.Spooled() {
		t.Fatal("a chunked artifact should be spooled, not inline")
	}
	if got := artifactText(t, env, ArtifactMHTML); got != body {
		t.Fatalf("reassembled = %q, want %q", got, body)
	}
	env.Cleanup()
	if countSpoolDirs(t, spool) != 0 {
		t.Fatalf("Cleanup left a spool directory behind in %s", spool)
	}
}

// spoolBytes totals the bytes sitting in every spool directory under root.
func spoolBytes(t *testing.T, root string) int64 {
	t.Helper()
	var total int64
	dirs, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	for _, d := range dirs {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), spoolPrefix) {
			continue
		}
		parts, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			t.Fatalf("read spool dir: %v", err)
		}
		for _, p := range parts {
			info, err := p.Info()
			if err != nil {
				t.Fatalf("stat part: %v", err)
			}
			total += info.Size()
		}
	}
	return total
}

func TestInlineArtifactIsNotSpooled(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "spool")
	a := NewAssembler(Options{SpoolDir: spool})
	env, err := a.Accept("c1", finalMsg(inlineArtifact(ArtifactReadable, "# small")))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if env.Artifacts[ArtifactReadable].Spooled() {
		t.Fatal("a single-message artifact should stay in memory")
	}
	// A capture that never chunked must not create a spool directory at all.
	if countSpoolDirs(t, spool) != 0 {
		t.Fatalf("an unchunked capture touched the disk: %s", spool)
	}
}

// TestFailedCaptureRemovesItsSpool: a capture that cannot complete must not
// leave its streamed bytes on disk.
func TestFailedCaptureRemovesItsSpool(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "spool")
	a := NewAssembler(Options{SpoolDir: spool})
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 2, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if countSpoolDirs(t, spool) != 1 {
		t.Fatal("chunk was not spooled")
	}
	if _, err := a.Accept("c1", finalEnvelope(chunkedEntry(ArtifactMHTML, 2))); err == nil {
		t.Fatal("expected an incomplete-artifact error")
	}
	if countSpoolDirs(t, spool) != 0 {
		t.Fatalf("a failed capture left its spool behind in %s", spool)
	}
}

func TestExpiredCaptureRemovesItsSpool(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "spool")
	a := NewAssembler(Options{SpoolDir: spool, ChunkTimeout: time.Minute})
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 2, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if dropped := a.Expire(time.Now().Add(2 * time.Minute)); len(dropped) != 1 {
		t.Fatalf("dropped = %v", dropped)
	}
	if countSpoolDirs(t, spool) != 0 {
		t.Fatalf("an expired capture left its spool behind in %s", spool)
	}
}

// TestDropRemovesItsSpool covers the path a timed-out CapturePage takes.
func TestDropRemovesItsSpool(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "spool")
	a := NewAssembler(Options{SpoolDir: spool})
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 2, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	a.Drop("c1")
	if countSpoolDirs(t, spool) != 0 {
		t.Fatalf("Drop left a spool directory behind in %s", spool)
	}
}

// TestDuplicateChunkIsNotRespooled: a retransmitted frame must be
// recognised from its digest, without a second copy on disk.
func TestDuplicateChunkIsNotRespooled(t *testing.T) {
	spool := filepath.Join(t.TempDir(), "spool")
	a := NewAssembler(Options{SpoolDir: spool})
	for i := 0; i < 3; i++ {
		if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 2, "aaa")); err != nil {
			t.Fatalf("resend %d: %v", i, err)
		}
	}
	if got := spoolBytes(t, spool); got != 3 {
		t.Fatalf("spooled %d bytes for three copies of one 3-byte chunk, want 3", got)
	}
}
