package capture

// Chunk handling: receiving a streamed artifact one frame at a time and
// putting its bytes on disk instead of in memory. The envelope assembly
// that consumes the result lives in assemble.go.

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// chunkSet tracks one artifact being streamed. The bytes are on disk; what
// is kept here is small and fixed per chunk — a path, a length and a digest
// — so an in-flight 100MB capture costs kilobytes of memory.
type chunkSet struct {
	total int
	parts map[int]chunkPart
	size  int64
}

// chunkPart is one spooled chunk. The digest is what makes a duplicate
// frame cheap to check without reading the part back.
type chunkPart struct {
	path string
	sum  [sha256.Size]byte
	size int64
}

func (c *chunkSet) complete() bool { return len(c.parts) == c.total }

// paths returns the spool files in index order. Only valid once complete.
func (c *chunkSet) paths() []string {
	out := make([]string, 0, c.total)
	for i := 0; i < c.total; i++ {
		out = append(out, c.parts[i].path)
	}
	return out
}

// spoolDir returns the capture's spool directory, creating it on first use
// so a capture that never chunks never touches the disk.
func (a *Assembler) spoolDir(st *inflight) (string, error) {
	if st.dir != "" {
		return st.dir, nil
	}
	root := a.opts.spoolRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("%w: %v", errNoSpoolDir, err)
	}
	dir, err := os.MkdirTemp(root, spoolPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errNoSpoolDir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("%w: %v", errNoSpoolDir, err)
	}
	st.dir = dir
	return dir, nil
}

// acceptChunk records one slice of one artifact. Called with a.mu held.
func (a *Assembler) acceptChunk(id string, data map[string]any) error {
	raw, _ := data["chunk"].(map[string]any)
	if raw == nil {
		return errors.New("chunk message: chunk is not an object")
	}
	name, _ := raw["of"].(string)
	if !ValidArtifactName(name) {
		return fmt.Errorf("chunk message: invalid artifact name %q", name)
	}
	total, okTotal := toInt(raw["total"])
	index, okIndex := toInt(raw["index"])
	if !okTotal || !okIndex {
		return fmt.Errorf("chunk message for %s: index/total are not numbers", name)
	}
	if total < 1 || total > maxChunks {
		return fmt.Errorf("chunk message for %s: total %d out of range (1..%d)", name, total, maxChunks)
	}
	if index < 0 || index >= total {
		return fmt.Errorf("chunk message for %s: index %d out of range (0..%d)", name, index, total-1)
	}

	encoded, _ := data["bytes"].(string)

	st := a.inflight[id]
	if st == nil {
		st = &inflight{chunks: make(map[string]*chunkSet)}
		a.inflight[id] = st
	}
	st.touched = time.Now()

	set := st.chunks[name]
	if set == nil {
		set = &chunkSet{total: total, parts: make(map[int]chunkPart)}
		st.chunks[name] = set
	}
	if set.total != total {
		return fmt.Errorf("chunk message for %s: total changed from %d to %d mid-stream", name, set.total, total)
	}

	// Budget the decoded size before decoding, so an oversize capture is
	// refused without ever materializing the bytes.
	if err := a.budget(st, int64(base64.StdEncoding.DecodedLen(len(encoded)))); err != nil {
		return fmt.Errorf("artifact %s: %w", name, err)
	}
	decoded, err := decodeBase64(encoded)
	if err != nil {
		return fmt.Errorf("chunk %d of %s: %w", index, name, err)
	}
	sum := sha256.Sum256(decoded)

	if prev, dup := set.parts[index]; dup {
		// A retransmitted chunk is harmless; a *different* chunk under the
		// same index means the two halves disagree about the payload, and
		// concatenating either one would produce a corrupt artifact.
		if prev.sum != sum {
			return fmt.Errorf("chunk %d of %s arrived twice with different bytes", index, name)
		}
		return nil
	}

	dir, err := a.spoolDir(st)
	if err != nil {
		return err
	}
	// This chunk is the only part of the artifact this process ever holds
	// in memory; from here it lives on disk until the envelope is written.
	path := filepath.Join(dir, fmt.Sprintf("%s.%04d.part", name, index))
	if err := writeSpoolPart(path, decoded); err != nil {
		return fmt.Errorf("chunk %d of %s: %w", index, name, err)
	}

	set.parts[index] = chunkPart{path: path, sum: sum, size: int64(len(decoded))}
	set.size += int64(len(decoded))
	st.total += int64(len(decoded))
	return nil
}

// verifyChunked checks a closing-message entry that says an artifact was
// streamed against the chunks that actually turned up.
func verifyChunked(name, encoded string, set *chunkSet, haveChunks bool, declared int, declaredOK bool) error {
	if encoded != "" {
		return fmt.Errorf("artifact %s is listed as chunked but also carries inline bytes", name)
	}
	if !haveChunks {
		return fmt.Errorf("artifact %s is listed as chunked but no chunks arrived", name)
	}
	if declaredOK && declared != set.total {
		return fmt.Errorf("artifact %s: the envelope declares %d chunks, the chunks themselves announced %d", name, declared, set.total)
	}
	if !set.complete() {
		return fmt.Errorf("artifact %s is incomplete: got %d of %d chunks", name, len(set.parts), set.total)
	}
	return nil
}
