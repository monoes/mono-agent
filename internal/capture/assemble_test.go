package capture

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// artifactText reads one artifact out of a finished envelope, whether it
// arrived inline or was spooled to disk in chunks — the point of the
// Artifact indirection is that a caller does not have to care which.
func artifactText(t *testing.T, env *Envelope, name string) string {
	t.Helper()
	artifact, ok := env.Artifacts[name]
	if !ok {
		t.Fatalf("envelope has no %s (has %v)", name, artifactNames(env))
	}
	body, err := artifact.Bytes()
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if got := int64(len(body)); got != artifact.Size() {
		t.Fatalf("%s: Size() = %d but read %d bytes", name, artifact.Size(), got)
	}
	return string(body)
}

func artifactNames(env *Envelope) []string {
	names := make([]string, 0, len(env.Artifacts))
	for name := range env.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// spoolTestOptions keeps a test's chunk spool inside its own temp dir,
// rather than the real ~/.monomind/inbox.
func spoolTestOptions(t *testing.T, opts Options) Options {
	t.Helper()
	if opts.SpoolDir == "" {
		opts.SpoolDir = filepath.Join(t.TempDir(), "spool")
	}
	return opts
}

// chunkMsg builds the `data` object of one chunk message.
func chunkMsg(of string, index, total int, body string) map[string]any {
	return map[string]any{
		"chunk": map[string]any{
			"of":    of,
			"index": float64(index),
			"total": float64(total),
		},
		"bytes": base64.StdEncoding.EncodeToString([]byte(body)),
	}
}

// finalMsg builds a closing message listing artifacts inline.
func finalMsg(artifacts ...map[string]any) map[string]any {
	list := make([]any, 0, len(artifacts))
	for _, a := range artifacts {
		list = append(list, a)
	}
	return map[string]any{
		"meta": map[string]any{
			"url":   "https://example.com/a",
			"title": "A",
		},
		"artifacts": list,
		"warnings":  []any{},
	}
}

func inlineArtifact(name, body string) map[string]any {
	return map[string]any{
		"name":     name,
		"encoding": "base64",
		"bytes":    base64.StdEncoding.EncodeToString([]byte(body)),
	}
}

func TestAcceptSingleMessageCapture(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	env, err := a.Accept("c1", finalMsg(inlineArtifact(ArtifactMHTML, "archive")))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if env == nil {
		t.Fatal("expected a finished envelope from a one-message capture")
	}
	if got := artifactText(t, env, ArtifactMHTML); got != "archive" {
		t.Fatalf("page.mhtml = %q, want %q", got, "archive")
	}
	if env.Meta.URL != "https://example.com/a" {
		t.Fatalf("meta.url = %q", env.Meta.URL)
	}
	if a.Pending("c1") {
		t.Fatal("finished capture left state behind")
	}
}

func TestAcceptChunkedArtifactOutOfOrder(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	// Deliberately shuffled, and the final message omits the artifact
	// entirely rather than listing it with empty bytes.
	for _, msg := range []map[string]any{
		chunkMsg(ArtifactMHTML, 2, 3, "ccc"),
		chunkMsg(ArtifactMHTML, 0, 3, "aaa"),
		chunkMsg(ArtifactMHTML, 1, 3, "bbb"),
	} {
		env, err := a.Accept("c1", msg)
		if err != nil {
			t.Fatalf("Accept chunk: %v", err)
		}
		if env != nil {
			t.Fatal("a chunk must not finish the capture")
		}
	}
	env, err := a.Accept("c1", finalMsg())
	if err != nil {
		t.Fatalf("Accept final: %v", err)
	}
	if got := artifactText(t, env, ArtifactMHTML); got != "aaabbbccc" {
		t.Fatalf("reassembled = %q, want %q", got, "aaabbbccc")
	}
}

func TestAcceptChunkedArtifactListedWithEmptyBytes(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", chunkMsg(ArtifactPDF, 0, 2, "pd")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if _, err := a.Accept("c1", chunkMsg(ArtifactPDF, 1, 2, "f!")); err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	final := finalMsg(map[string]any{"name": ArtifactPDF, "encoding": "base64", "bytes": ""})
	env, err := a.Accept("c1", final)
	if err != nil {
		t.Fatalf("Accept final: %v", err)
	}
	if got := artifactText(t, env, ArtifactPDF); got != "pdf!" {
		t.Fatalf("reassembled = %q, want %q", got, "pdf!")
	}
}

// chunkedEntry is how the closing message lists an artifact that was
// streamed: a receipt with the chunk count and no bytes.
func chunkedEntry(name string, chunks int) map[string]any {
	return map[string]any{"name": name, "encoding": "base64", "chunked": true, "chunks": float64(chunks)}
}

// finalEnvelope is finalMsg with the sender's `final: true` marker, exactly
// as the extension emits it.
func finalEnvelope(artifacts ...map[string]any) map[string]any {
	msg := finalMsg(artifacts...)
	msg["final"] = true
	return msg
}

// TestAcceptChunkedReceiptIsVerified: the closing message declares how many
// chunks were sent, and that count is what turns a dropped frame into an
// error instead of a truncated file on disk.
func TestAcceptChunkedReceiptIsVerified(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	for i, body := range []string{"aaa", "bbb", "ccc"} {
		if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, i, 3, body)); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
	env, err := a.Accept("c1", finalEnvelope(chunkedEntry(ArtifactMHTML, 3)))
	if err != nil {
		t.Fatalf("Accept final: %v", err)
	}
	if got := artifactText(t, env, ArtifactMHTML); got != "aaabbbccc" {
		t.Fatalf("reassembled = %q", got)
	}
}

func TestAcceptChunkedReceiptCatchesALostFrame(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	// The sender streamed three; only two survived the trip.
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 3, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 1, 3, "bbb")); err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	_, err := a.Accept("c1", finalEnvelope(chunkedEntry(ArtifactMHTML, 3)))
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("err = %v, want an incomplete-artifact error", err)
	}
}

func TestAcceptChunkedReceiptCatchesACountMismatch(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 1, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	_, err := a.Accept("c1", finalEnvelope(chunkedEntry(ArtifactMHTML, 4)))
	if err == nil || !strings.Contains(err.Error(), "declares 4 chunks") {
		t.Fatalf("err = %v, want a chunk-count mismatch error", err)
	}
}

func TestAcceptChunkedReceiptWithoutChunksFails(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	_, err := a.Accept("c1", finalEnvelope(chunkedEntry(ArtifactPDF, 2)))
	if err == nil || !strings.Contains(err.Error(), "no chunks arrived") {
		t.Fatalf("err = %v, want a missing-chunks error", err)
	}
}

func TestAcceptChunkedReceiptWithInlineBytesFails(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 1, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	entry := chunkedEntry(ArtifactMHTML, 1)
	entry["bytes"] = base64.StdEncoding.EncodeToString([]byte("aaa"))
	_, err := a.Accept("c1", finalEnvelope(entry))
	if err == nil || !strings.Contains(err.Error(), "also carries inline bytes") {
		t.Fatalf("err = %v, want a chunked/inline conflict error", err)
	}
}

// TestAcceptRejectsChunkClaimingToBeFinal guards the one frame shape the
// contract forbids outright.
func TestAcceptRejectsChunkClaimingToBeFinal(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	msg := chunkMsg(ArtifactMHTML, 0, 2, "aaa")
	msg["final"] = true
	if _, err := a.Accept("c1", msg); err == nil {
		t.Fatal("accepted a frame that is both a chunk and the envelope")
	}
}

func TestAcceptDuplicateChunkIsIdempotent(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	for i := 0; i < 3; i++ {
		if _, err := a.Accept("c1", chunkMsg(ArtifactReadable, 0, 2, "hello ")); err != nil {
			t.Fatalf("resend %d: %v", i, err)
		}
	}
	if _, err := a.Accept("c1", chunkMsg(ArtifactReadable, 1, 2, "world")); err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	env, err := a.Accept("c1", finalMsg())
	if err != nil {
		t.Fatalf("Accept final: %v", err)
	}
	if got := artifactText(t, env, ArtifactReadable); got != "hello world" {
		t.Fatalf("reassembled = %q", got)
	}
}

func TestAcceptConflictingDuplicateChunkFails(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", chunkMsg(ArtifactReadable, 0, 2, "one")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	_, err := a.Accept("c1", chunkMsg(ArtifactReadable, 0, 2, "two"))
	if err == nil || !strings.Contains(err.Error(), "different bytes") {
		t.Fatalf("err = %v, want a conflicting-duplicate error", err)
	}
	if a.Pending("c1") {
		t.Fatal("a failed capture must not keep buffering")
	}
}

func TestAcceptMissingChunkFailsAtFinalMessage(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 3, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 2, 3, "ccc")); err != nil {
		t.Fatalf("chunk 2: %v", err)
	}
	_, err := a.Accept("c1", finalMsg())
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("err = %v, want an incomplete-artifact error", err)
	}
}

func TestExpireDropsStalledCaptureAndRejectsItsTail(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{ChunkTimeout: time.Minute}))
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 2, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	if dropped := a.Expire(time.Now()); len(dropped) != 0 {
		t.Fatalf("dropped a fresh capture: %v", dropped)
	}
	dropped := a.Expire(time.Now().Add(2 * time.Minute))
	if len(dropped) != 1 || dropped[0] != "c1" {
		t.Fatalf("dropped = %v, want [c1]", dropped)
	}
	if a.Pending("c1") {
		t.Fatal("expired capture still buffered")
	}
	// The straggler must not be written to the inbox as a capture that is
	// silently missing page.mhtml.
	if _, err := a.Accept("c1", finalMsg()); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestAcceptRefusesOversizeCapture(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{MaxBytes: 1024}))
	big := strings.Repeat("x", 800)
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 2, big)); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	_, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 1, 2, big))
	if err == nil || !strings.Contains(err.Error(), "1024 byte limit") {
		t.Fatalf("err = %v, want a size-limit error", err)
	}
	if a.Pending("c1") {
		t.Fatal("oversize capture still buffered")
	}
}

func TestAcceptRefusesOversizeInlineArtifact(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{MaxBytes: 64}))
	_, err := a.Accept("c1", finalMsg(inlineArtifact(ArtifactMHTML, strings.Repeat("x", 200))))
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("err = %v, want a size-limit error", err)
	}
}

func TestAcceptRejectsUnsafeArtifactNames(t *testing.T) {
	for _, name := range []string{"../escape.mhtml", "sub/dir.png", "", ".hidden", "meta.json"} {
		t.Run(name, func(t *testing.T) {
			a := NewAssembler(spoolTestOptions(t, Options{}))
			if _, err := a.Accept("c1", finalMsg(inlineArtifact(name, "x"))); err == nil {
				t.Fatalf("accepted unsafe artifact name %q", name)
			}
			a2 := NewAssembler(spoolTestOptions(t, Options{}))
			if _, err := a2.Accept("c1", chunkMsg(name, 0, 1, "x")); err == nil {
				t.Fatalf("accepted unsafe chunk name %q", name)
			}
		})
	}
}

func TestAcceptRejectsBadChunkHeaders(t *testing.T) {
	cases := map[string]map[string]any{
		"index past total": chunkMsg(ArtifactMHTML, 3, 3, "x"),
		"zero total": {
			"chunk": map[string]any{"of": ArtifactMHTML, "index": float64(0), "total": float64(0)},
			"bytes": "",
		},
		"non-numeric index": {
			"chunk": map[string]any{"of": ArtifactMHTML, "index": "first", "total": float64(2)},
			"bytes": "",
		},
		"chunk not an object": {"chunk": "page.mhtml", "bytes": ""},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			a := NewAssembler(spoolTestOptions(t, Options{}))
			if _, err := a.Accept("c1", msg); err == nil {
				t.Fatal("accepted a malformed chunk header")
			}
		})
	}
}

func TestAcceptRejectsTotalChangingMidStream(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 3, "a")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	_, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 1, 4, "b"))
	if err == nil || !strings.Contains(err.Error(), "total changed") {
		t.Fatalf("err = %v, want a total-changed error", err)
	}
}

func TestAcceptRejectsChunkedAndInlineSameArtifact(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 2, "a")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	_, err := a.Accept("c1", finalMsg(inlineArtifact(ArtifactMHTML, "whole thing")))
	if err == nil || !strings.Contains(err.Error(), "both chunked and inline") {
		t.Fatalf("err = %v, want a chunked/inline conflict error", err)
	}
}

func TestAcceptConcurrentCapturesDoNotMix(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("requested", chunkMsg(ArtifactMHTML, 0, 2, "req-")); err != nil {
		t.Fatalf("requested chunk 0: %v", err)
	}
	if _, err := a.Accept("flushed", chunkMsg(ArtifactMHTML, 0, 2, "queued-")); err != nil {
		t.Fatalf("flushed chunk 0: %v", err)
	}
	if _, err := a.Accept("flushed", chunkMsg(ArtifactMHTML, 1, 2, "two")); err != nil {
		t.Fatalf("flushed chunk 1: %v", err)
	}
	if _, err := a.Accept("requested", chunkMsg(ArtifactMHTML, 1, 2, "one")); err != nil {
		t.Fatalf("requested chunk 1: %v", err)
	}
	flushed, err := a.Accept("flushed", finalMsg())
	if err != nil {
		t.Fatalf("flushed final: %v", err)
	}
	requested, err := a.Accept("requested", finalMsg())
	if err != nil {
		t.Fatalf("requested final: %v", err)
	}
	if got := artifactText(t, flushed, ArtifactMHTML); got != "queued-two" {
		t.Fatalf("flushed = %q", got)
	}
	if got := artifactText(t, requested, ArtifactMHTML); got != "req-one" {
		t.Fatalf("requested = %q", got)
	}
}

func TestAcceptTextEncodedArtifact(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	env, err := a.Accept("c1", finalMsg(map[string]any{
		"name":     ArtifactReadable,
		"encoding": "utf8",
		"bytes":    "# Title\n\nbody",
	}))
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got := artifactText(t, env, ArtifactReadable); got != "# Title\n\nbody" {
		t.Fatalf("readable.md = %q", got)
	}
}

func TestAcceptNilDataFails(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", nil); err == nil {
		t.Fatal("accepted a response with no data")
	}
}

func TestAcceptCollectsWarnings(t *testing.T) {
	a := NewAssembler(spoolTestOptions(t, Options{}))
	msg := finalMsg(inlineArtifact(ArtifactMHTML, "x"))
	msg["warnings"] = []any{"pdf unavailable on this page", 42}
	env, err := a.Accept("c1", msg)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if len(env.Warnings) != 1 || env.Warnings[0] != "pdf unavailable on this page" {
		t.Fatalf("warnings = %v", env.Warnings)
	}
}

// TestAcceptDecodesRealJSON pins the assembler to what actually comes off
// the wire (float64 numbers from encoding/json) rather than the hand-built
// maps the other tests use.
func TestAcceptDecodesRealJSON(t *testing.T) {
	raw := `{"chunk":{"index":1,"total":2,"of":"page.mhtml"},"bytes":"YmJi"}`
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	a := NewAssembler(spoolTestOptions(t, Options{}))
	if _, err := a.Accept("c1", data); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := a.Accept("c1", chunkMsg(ArtifactMHTML, 0, 2, "aaa")); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	env, err := a.Accept("c1", finalMsg())
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	if got := artifactText(t, env, ArtifactMHTML); got != "aaabbb" {
		t.Fatalf("reassembled = %q", got)
	}
}
