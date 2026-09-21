package captureexport

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/capture"
)

// Collision says what to do when a capture in the archive has the same
// directory name as one already in the inbox.
type Collision string

const (
	// CollisionSkip leaves the existing capture alone. It is the default:
	// an import is additive, and a re-import of an overlapping archive
	// should be a no-op rather than a rewrite.
	CollisionSkip Collision = "skip"
	// CollisionRename keeps both, giving the incoming one a suffix.
	CollisionRename Collision = "rename"
	// CollisionOverwrite replaces the existing capture.
	CollisionOverwrite Collision = "overwrite"
)

// ParseCollision validates the --on-collision value.
func ParseCollision(s string) (Collision, error) {
	switch Collision(strings.ToLower(strings.TrimSpace(s))) {
	case "", CollisionSkip:
		return CollisionSkip, nil
	case CollisionRename:
		return CollisionRename, nil
	case CollisionOverwrite:
		return CollisionOverwrite, nil
	}
	return "", fmt.Errorf("captureexport: unknown collision mode %q; want skip, rename or overwrite", s)
}

// Import limits. A capture is allowed to be large — an MHTML of a heavy
// page genuinely is — but an archive claiming a 40GB file is not an import,
// it is a disk-filling attack, and this reads archives that arrived from
// somewhere else by definition.
const (
	maxFileBytes  int64 = 2 << 30  // 2 GiB in one artifact
	maxTotalBytes int64 = 64 << 30 // 64 GiB in one archive
)

// ImportOptions configures a restore.
type ImportOptions struct {
	// Inbox is where captures are restored to; empty means the default.
	Inbox string
	// OnCollision decides what happens to a name that already exists.
	OnCollision Collision
	// DryRun reports what would happen and writes nothing.
	DryRun bool
}

// Imported is one capture that landed (or would have).
type Imported struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	Bytes    int64  `json:"bytes"`
	Renamed  bool   `json:"renamed,omitempty"`
	Replaced bool   `json:"replaced,omitempty"`
}

// ImportResult is what an import did.
type ImportResult struct {
	Inbox    string     `json:"inbox"`
	Manifest *Manifest  `json:"manifest,omitempty"`
	Imported []Imported `json:"imported"`
	Skipped  []Note     `json:"skipped,omitempty"`
	DryRun   bool       `json:"dryRun,omitempty"`
}

// Import restores the captures in a gzipped tar archive into the inbox.
//
// Each capture is unpacked into a staging directory inside the inbox and
// renamed into place only once it is complete, the same discipline
// capture.Writer uses — an interrupted import leaves staging directories
// behind, never a half-written capture for a watcher to ingest.
func Import(r io.Reader, opts ImportOptions) (*ImportResult, error) {
	inbox := opts.Inbox
	if strings.TrimSpace(inbox) == "" {
		inbox = capture.DefaultInbox()
	}
	mode := opts.OnCollision
	if mode == "" {
		mode = CollisionSkip
	}
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		return nil, fmt.Errorf("create inbox %s: %w", inbox, err)
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()

	res := &ImportResult{Inbox: inbox, Imported: []Imported{}, DryRun: opts.DryRun}
	imp := &importer{opts: opts, inbox: inbox, mode: mode, res: res, staged: map[string]string{}}
	defer imp.cleanup()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if err := imp.entry(hdr, tr); err != nil {
			return nil, err
		}
	}
	if !imp.seenManifest && len(imp.staged) == 0 {
		return nil, errors.New("captureexport: this is not a capture archive — no manifest.json and no captures/")
	}
	if err := imp.commit(); err != nil {
		return nil, err
	}
	sort.Slice(res.Imported, func(i, j int) bool { return res.Imported[i].Name < res.Imported[j].Name })
	return res, nil
}

// importer holds the state of one import: the staging directory per
// capture name, until they are all unpacked and can be renamed into place.
type importer struct {
	opts         ImportOptions
	inbox        string
	mode         Collision
	res          *ImportResult
	staged       map[string]string // capture name -> staging directory
	total        int64
	seenManifest bool
}

// entry handles one tar member.
func (im *importer) entry(hdr *tar.Header, tr io.Reader) error {
	name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
	switch hdr.Typeflag {
	case tar.TypeReg, tar.TypeDir:
	default:
		// A capture is files in a flat directory. A symlink, a device node
		// or a hard link in this archive is either corruption or an
		// attempt to write outside the inbox; neither gets unpacked.
		return nil
	}
	if name == ManifestPath {
		return im.readManifest(tr)
	}
	if name == ReadmePath {
		return nil
	}
	dir, file, ok := splitCapturePath(name)
	if !ok {
		return nil // anything outside captures/<name>/<file> is not ours
	}
	if hdr.Typeflag == tar.TypeDir || file == "" {
		return nil // the directory entry itself; the files create it
	}
	if hdr.Size > maxFileBytes {
		return fmt.Errorf("captureexport: %s is %d bytes, past the %d byte limit", name, hdr.Size, maxFileBytes)
	}
	im.total += hdr.Size
	if im.total > maxTotalBytes {
		return fmt.Errorf("captureexport: archive is larger than the %d byte limit", maxTotalBytes)
	}

	staging, err := im.stagingFor(dir)
	if err != nil {
		return err
	}
	if staging == "" {
		return im.drain(tr) // already decided to skip this capture
	}
	return writeStagedFile(filepath.Join(staging, file), tr, hdr.Size)
}

func (im *importer) readManifest(tr io.Reader) error {
	im.seenManifest = true
	blob, err := io.ReadAll(io.LimitReader(tr, 64<<20))
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var man Manifest
	if err := json.Unmarshal(blob, &man); err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	if man.Format != Format {
		return fmt.Errorf("captureexport: %q is not a capture archive (format %q)", ManifestPath, man.Format)
	}
	if man.Version > Version {
		return fmt.Errorf("captureexport: archive version %d is newer than this build understands (%d)", man.Version, Version)
	}
	im.res.Manifest = &man
	return nil
}

// stagingFor returns the staging directory for a capture name, creating it
// the first time the name is seen, or "" if this capture is being skipped.
func (im *importer) stagingFor(name string) (string, error) {
	if dir, ok := im.staged[name]; ok {
		return dir, nil
	}
	if !validCaptureName(name) {
		im.skip(name, "unsafe directory name")
		im.staged[name] = ""
		return "", nil
	}
	if _, err := os.Lstat(filepath.Join(im.inbox, name)); err == nil && im.mode == CollisionSkip {
		im.skip(name, "already in the inbox")
		im.staged[name] = ""
		return "", nil
	}
	dir, err := os.MkdirTemp(im.inbox, ".tmp-import-*")
	if err != nil {
		return "", fmt.Errorf("create staging dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("secure staging dir: %w", err)
	}
	im.staged[name] = dir
	return dir, nil
}

// commit renames every staged capture into the inbox. A capture without a
// meta.json is dropped here rather than published: the inbox contract is
// that a directory with a meta.json is a landed capture.
func (im *importer) commit() error {
	names := make([]string, 0, len(im.staged))
	for name := range im.staged {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		staging := im.staged[name]
		if staging == "" {
			continue
		}
		meta, err := readMeta(staging)
		if err != nil {
			im.skip(name, "no readable "+capture.MetaFile)
			continue
		}
		bytes, err := dirBytes(staging)
		if err != nil {
			return err
		}
		dest, renamed, err := im.destination(name)
		if err != nil {
			return err
		}
		entry := Imported{
			Name: filepath.Base(dest), Path: dest, URL: meta.DedupeURL(),
			Title: meta.Title, Bytes: bytes, Renamed: renamed,
		}
		if im.opts.DryRun {
			im.res.Imported = append(im.res.Imported, entry)
			continue
		}
		if im.mode == CollisionOverwrite && !renamed {
			if _, err := os.Lstat(dest); err == nil {
				if err := os.RemoveAll(dest); err != nil {
					return fmt.Errorf("replace %s: %w", dest, err)
				}
				entry.Replaced = true
			}
		}
		if err := os.Rename(staging, dest); err != nil {
			return fmt.Errorf("publish %s: %w", dest, err)
		}
		delete(im.staged, name)
		im.res.Imported = append(im.res.Imported, entry)
	}
	return nil
}

// destination resolves the final directory for a capture, stepping past an
// existing name when renaming.
func (im *importer) destination(name string) (string, bool, error) {
	dest := filepath.Join(im.inbox, name)
	if _, err := os.Lstat(dest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dest, false, nil
		}
		return "", false, fmt.Errorf("check %s: %w", dest, err)
	}
	if im.mode != CollisionRename {
		return dest, false, nil
	}
	for n := 2; n < 1000; n++ {
		candidate := fmt.Sprintf("%s-%d", dest, n)
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, true, nil
		}
	}
	return "", false, fmt.Errorf("captureexport: no free name for %s", name)
}

func (im *importer) skip(name, reason string) {
	im.res.Skipped = append(im.res.Skipped, Note{Name: name, Reason: reason})
}

func (im *importer) drain(r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}

// cleanup removes staging directories that were never committed — a failed
// import, or a dry run.
func (im *importer) cleanup() {
	for _, dir := range im.staged {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
	}
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
