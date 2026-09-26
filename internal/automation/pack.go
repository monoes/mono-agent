package automation

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing/fstest"
	"time"
)

// Size caps for untrusted packages (zip archives, URL downloads and
// directories being installed).
var (
	MaxArchiveBytes int64 = 20 << 20 // .mpkg / download size
	MaxFileBytes    int64 = 10 << 20 // one extracted file
	MaxTotalBytes   int64 = 50 << 20 // all extracted files
	MaxFiles              = 2000
)

// ChecksumsFile is the generated per-file sha256 list inside a .mpkg.
const ChecksumsFile = "CHECKSUMS"

// zipEpoch is the fixed modification time of every exported entry, so the
// same files always produce the same bytes.
var zipEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// ErrUnsafeArchive marks an archive rejected by the safety checks.
var ErrUnsafeArchive = errors.New("unsafe package archive")

// readZip reads a .mpkg from r into memory after checking every entry:
// no absolute or parent-relative paths, no symlinks or special files, no
// duplicates, file-count and size caps, and CHECKSUMS when present.
func readZip(r io.Reader) (fs.FS, error) {
	fsys, _, err := readZipSum(r)
	return fsys, err
}

// readZipSum is readZip that also returns the sha256 of the archive bytes
// it read (the bytes the review describes).
func readZipSum(r io.Reader) (fs.FS, string, error) {
	raw, err := io.ReadAll(io.LimitReader(r, MaxArchiveBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(raw)) > MaxArchiveBytes {
		return nil, "", fmt.Errorf("%w: archive larger than %d bytes", ErrUnsafeArchive, MaxArchiveBytes)
	}
	fsys, err := readZipBytes(raw)
	if err != nil {
		return nil, "", err
	}
	return fsys, sha256Hex(raw), nil
}

func readZipBytes(raw []byte) (fs.FS, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	if zr == nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	files := map[string][]byte{}
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if err := checkEntryName(name); err != nil {
			return nil, err
		}
		mode := f.Mode()
		if mode.IsDir() {
			continue
		}
		if !mode.IsRegular() {
			return nil, fmt.Errorf("%w: %s is not a regular file (%s)", ErrUnsafeArchive, name, mode.Type())
		}
		if _, dup := files[name]; dup {
			return nil, fmt.Errorf("%w: duplicate entry %s", ErrUnsafeArchive, name)
		}
		if len(files) >= MaxFiles {
			return nil, fmt.Errorf("%w: more than %d files", ErrUnsafeArchive, MaxFiles)
		}
		if f.UncompressedSize64 > uint64(MaxFileBytes) {
			return nil, fmt.Errorf("%w: %s larger than %d bytes", ErrUnsafeArchive, name, MaxFileBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		// The header's size can lie; cap what is actually inflated.
		b, err := io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if int64(len(b)) > MaxFileBytes {
			return nil, fmt.Errorf("%w: %s larger than %d bytes", ErrUnsafeArchive, name, MaxFileBytes)
		}
		total += int64(len(b))
		if total > MaxTotalBytes {
			return nil, fmt.Errorf("%w: contents larger than %d bytes", ErrUnsafeArchive, MaxTotalBytes)
		}
		files[name] = b
	}
	if err := verifyChecksums(files); err != nil {
		return nil, err
	}
	return mapFS(files), nil
}

func checkEntryName(name string) error {
	clean := strings.TrimSuffix(name, "/")
	switch {
	case clean == "":
		return fmt.Errorf("%w: empty entry name", ErrUnsafeArchive)
	case strings.Contains(name, `\`):
		return fmt.Errorf("%w: backslash in entry %q", ErrUnsafeArchive, name)
	case strings.HasPrefix(name, "/") || filepath.IsAbs(name) || (len(name) > 1 && name[1] == ':'):
		return fmt.Errorf("%w: absolute entry path %q", ErrUnsafeArchive, name)
	case !fs.ValidPath(clean):
		return fmt.Errorf("%w: entry path %q escapes the package", ErrUnsafeArchive, name)
	}
	return nil
}

// verifyChecksums checks CHECKSUMS (when present) against files: every
// listed file must exist and match, and every file must be listed.
// CHECKSUMS itself is dropped from files.
func verifyChecksums(files map[string][]byte) error {
	sums, ok := files[ChecksumsFile]
	if !ok {
		return nil
	}
	delete(files, ChecksumsFile)
	listed := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 {
			return fmt.Errorf("%w: malformed CHECKSUMS line %q", ErrUnsafeArchive, line)
		}
		b, exists := files[name]
		if !exists {
			return fmt.Errorf("%w: CHECKSUMS lists missing file %s", ErrUnsafeArchive, name)
		}
		if sha256Hex(b) != strings.ToLower(sum) {
			return fmt.Errorf("%w: checksum mismatch for %s", ErrUnsafeArchive, name)
		}
		listed[name] = true
	}
	for name := range files {
		if !listed[name] {
			return fmt.Errorf("%w: %s is not listed in CHECKSUMS", ErrUnsafeArchive, name)
		}
	}
	return nil
}

func mapFS(files map[string][]byte) fs.FS {
	m := fstest.MapFS{}
	for name, b := range files {
		m[name] = &fstest.MapFile{Data: b, Mode: 0o644, ModTime: zipEpoch}
	}
	return m
}

// readTree reads every regular file of fsys (dotfiles skipped) into memory.
func readTree(fsys fs.FS) (map[string][]byte, error) {
	names, err := listFiles(fsys)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(names))
	for _, n := range names {
		if n == ChecksumsFile {
			continue
		}
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, err
		}
		out[n] = b
	}
	return out, nil
}

// snapshotDir reads an untrusted package directory into memory under the
// same rules as an archive: no symlinks or special files, caps enforced.
func snapshotDir(dir string) (fs.FS, error) {
	files := map[string][]byte{}
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel != "." && strings.HasPrefix(path.Base(rel), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file", ErrUnsafeArchive, rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxFileBytes {
			return fmt.Errorf("%w: %s larger than %d bytes", ErrUnsafeArchive, rel, MaxFileBytes)
		}
		if total += info.Size(); total > MaxTotalBytes {
			return fmt.Errorf("%w: contents larger than %d bytes", ErrUnsafeArchive, MaxTotalBytes)
		}
		if len(files) >= MaxFiles {
			return fmt.Errorf("%w: more than %d files", ErrUnsafeArchive, MaxFiles)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := verifyChecksums(files); err != nil {
		return nil, err
	}
	return mapFS(files), nil
}

// Pack writes the package directory dir as a .mpkg zip (with CHECKSUMS) to w.
func Pack(dir string, w io.Writer) error {
	fsys, err := snapshotDir(dir)
	if err != nil {
		return err
	}
	if _, err := readManifest(fsys); err != nil {
		return err
	}
	files, err := readTree(fsys)
	if err != nil {
		return err
	}
	return writeZip(files, w)
}

// writeZip writes files as a deterministic zip: CHECKSUMS first, then the
// files in sorted order, fixed timestamps and modes.
func writeZip(files map[string][]byte, w io.Writer) error {
	names := make([]string, 0, len(files))
	for n := range files {
		if n != ChecksumsFile {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, n := range names {
		fmt.Fprintf(&sums, "%s  %s\n", sha256Hex(files[n]), n)
	}
	zw := zip.NewWriter(w)
	put := func(name string, b []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
		h.SetMode(0o644)
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = fw.Write(b)
		return err
	}
	if err := put(ChecksumsFile, []byte(sums.String())); err != nil {
		return err
	}
	for _, n := range names {
		if err := put(n, files[n]); err != nil {
			return err
		}
	}
	return zw.Close()
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// treeHash is the package content hash: sha256 over the sorted
// "path\x00sha256(content)\n" lines of every file (CHECKSUMS excluded).
func treeHash(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		if n != ChecksumsFile {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s\x00%s\n", n, sha256Hex(files[n]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fsHash is treeHash over a file system.
func fsHash(fsys fs.FS) (string, error) {
	files, err := readTree(fsys)
	if err != nil {
		return "", err
	}
	return treeHash(files), nil
}

// fileInfos lists files with size and sha256 for the install review.
func fileInfos(files map[string][]byte) []FileInfo {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]FileInfo, 0, len(names))
	for _, n := range names {
		out = append(out, FileInfo{Path: n, Size: int64(len(files[n])), SHA256: sha256Hex(files[n])})
	}
	return out
}
