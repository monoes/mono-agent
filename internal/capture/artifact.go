package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Artifact is one file in a capture envelope.
//
// An artifact small enough to arrive in a single message is held in memory —
// it was already buffered whole as part of that WebSocket frame, so nothing
// is saved by doing otherwise. An artifact large enough to be streamed in
// chunks is spooled to disk as each chunk lands and is never held whole in
// RAM: a legal capture may be 100MB, and carrying that per in-flight
// capture is a cost this process should not pay when the bytes are on their
// way to a file anyway.
//
// The zero value is a valid, empty artifact.
type Artifact struct {
	// inline holds the bytes when they arrived in one message.
	inline []byte
	// parts are the spool files to concatenate, already in index order,
	// when the artifact was streamed.
	parts []string
	size  int64
}

// Inline builds an artifact from bytes already in memory.
func Inline(b []byte) Artifact { return Artifact{inline: b, size: int64(len(b))} }

// spooledArtifact builds an artifact from part files, in order.
func spooledArtifact(parts []string, size int64) Artifact {
	return Artifact{parts: parts, size: size}
}

// Size is the artifact's length in bytes, known without reading it.
func (a Artifact) Size() int64 { return a.size }

// Spooled reports whether the artifact lives on disk rather than in memory.
func (a Artifact) Spooled() bool { return len(a.parts) > 0 }

// Bytes reads the whole artifact into memory. It is for consumers that
// genuinely need every byte at once (and for tests); the writer streams
// instead, and the hash is computed without it.
func (a Artifact) Bytes() ([]byte, error) {
	if !a.Spooled() {
		return a.inline, nil
	}
	buf := make([]byte, 0, a.size)
	for _, part := range a.parts {
		b, err := os.ReadFile(part)
		if err != nil {
			return nil, fmt.Errorf("read spooled chunk %s: %w", filepath.Base(part), err)
		}
		buf = append(buf, b...)
	}
	return buf, nil
}

// copyBufSize is the window used to move a spooled artifact into place.
const copyBufSize = 256 << 10

// writeTo writes the artifact to path and flushes it, so the rename that
// publishes a capture cannot expose a file whose contents are still only in
// the page cache. 0600: captures routinely contain the contents of
// logged-in pages. A spooled artifact is streamed part by part, never
// assembled in memory first.
func (a Artifact) writeTo(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}
	if err := a.stream(f); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("flush %s: %w", filepath.Base(path), err)
	}
	return f.Close()
}

// stream writes the artifact's bytes to w in order.
func (a Artifact) stream(w io.Writer) error {
	if !a.Spooled() {
		_, err := w.Write(a.inline)
		return err
	}
	buf := make([]byte, copyBufSize)
	for _, part := range a.parts {
		src, err := os.Open(part)
		if err != nil {
			return fmt.Errorf("reopen spooled chunk %s: %w", filepath.Base(part), err)
		}
		n, err := io.CopyBuffer(w, src, buf)
		src.Close()
		if err != nil {
			return fmt.Errorf("copy spooled chunk %s: %w", filepath.Base(part), err)
		}
		if n == 0 && a.size > 0 {
			return fmt.Errorf("spooled chunk %s is empty", filepath.Base(part))
		}
	}
	return nil
}

// hash returns the artifact's content hash in the envelope's
// "sha256:<hex>" form, streaming rather than buffering.
func (a Artifact) hash() (string, error) {
	h := sha256.New()
	if err := a.stream(h); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// writeSpoolPart writes one decoded chunk into the spool directory. Parts
// are not flushed: a process that dies mid-capture loses that capture
// regardless, and the durability that matters is applied to the assembled
// file in the staging directory.
func writeSpoolPart(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("spool chunk: %w", err)
	}
	return nil
}

// errNoSpoolDir is returned when a chunk arrives but the spool directory
// could not be created, so there is nowhere to stream it.
var errNoSpoolDir = errors.New("capture: no spool directory available")
