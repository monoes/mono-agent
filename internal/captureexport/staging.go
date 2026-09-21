package captureexport

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/capture"
)

// Unpacking one capture: where the archive's names are checked against
// what the inbox will accept, and where a file is written — or, in a dry
// run, read past without being written.

// inspect is the dry run's version of unpacking: meta.json is read into
// memory, because the report needs the URL and title, and everything else
// is read past without being written.
func (s *staging) inspect(file string, tr io.Reader) error {
	if file != capture.MetaFile {
		return drain(tr)
	}
	blob, err := io.ReadAll(io.LimitReader(tr, maxMetaBytes))
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	s.meta = blob
	return drain(tr)
}

// readMeta reads the capture's provenance record: off disk for a real
// import, out of memory for a dry run.
func (s *staging) readMeta() (*capture.Meta, error) {
	if s.dir != "" {
		return capture.ReadMeta(s.dir)
	}
	if len(s.meta) == 0 {
		return nil, os.ErrNotExist
	}
	var meta capture.Meta
	if err := json.Unmarshal(s.meta, &meta); err != nil {
		return nil, fmt.Errorf("decode %s: %w", capture.MetaFile, err)
	}
	return &meta, nil
}

func drain(r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}

// writeStagedFile writes one artifact into a staging directory, refusing to
// read more than the header promised.
func writeStagedFile(dest string, r io.Reader, size int64) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(dest), err)
	}
	if _, err := io.Copy(f, io.LimitReader(r, size)); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(dest), err)
	}
	return f.Close()
}

// splitCapturePath pulls "captures/<dir>/<file>" apart, rejecting anything
// deeper or anywhere else.
func splitCapturePath(name string) (dir, file string, ok bool) {
	rest, found := strings.CutPrefix(name, CapturesDir+"/")
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		return parts[0], "", parts[0] != ""
	case 2:
		return parts[0], parts[1], parts[0] != ""
	}
	return "", "", false
}

// validCaptureName reports whether a directory name from the archive is
// safe to create inside the inbox. It is held to the same rules the inbox
// itself uses: no separators, no traversal, and no dot prefix — a
// dot-prefixed directory is what the inbox uses for staging, and a watcher
// skips it.
func validCaptureName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	return name == filepath.Base(name)
}

func dirBytes(dir string) (int64, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dir, err)
	}
	var total int64
	for _, f := range files {
		info, err := f.Info()
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}
