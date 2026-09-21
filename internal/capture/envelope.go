package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// Envelope is one finished capture, ready to be written to the inbox.
//
// An envelope whose artifacts were streamed owns a spool directory holding
// their bytes. Write removes it; a caller that decides not to write must
// call Cleanup instead, or those bytes are leaked until the next sweep.
type Envelope struct {
	Meta      Meta
	Artifacts map[string]Artifact
	Warnings  []string

	spoolDir string
}

// Cleanup removes anything the envelope spooled to disk. It is idempotent,
// and a no-op for an envelope that arrived in a single message.
func (e *Envelope) Cleanup() {
	if e == nil || e.spoolDir == "" {
		return
	}
	_ = os.RemoveAll(e.spoolDir)
	e.spoolDir = ""
}

// Result describes an envelope that has landed on disk. It is also the JSON
// the `capture page --json` command prints and the relay hands back to a
// calling process.
type Result struct {
	Path      string   `json:"path"`
	Meta      Meta     `json:"meta"`
	Artifacts []string `json:"artifacts"`
	Bytes     int64    `json:"bytes"`
	Warnings  []string `json:"warnings,omitempty"`
}

// MetaFile is the provenance file every envelope directory contains; a
// directory without one is a half-written capture and must be ignored.
const MetaFile = "meta.json"

// The canonical artifact names from the plan. Other names are allowed (a
// per-site adapter may add table.csv or transcript.md) as long as they pass
// ValidArtifactName; these are the ones the pipeline looks for by name.
const (
	ArtifactMHTML = "page.mhtml"
	// ArtifactHTML is the byte-fidelity artifact for a capture made
	// without a browser: the crawler has no page to snapshot, so it keeps
	// the HTML the server actually sent instead of an MHTML archive.
	ArtifactHTML       = "page.html"
	ArtifactPDF        = "page.pdf"
	ArtifactReadable   = "readable.md"
	ArtifactScreenshot = "screenshot.png"
)

// hashPreference is the order in which artifacts are considered when the
// sender did not compute a content hash: the readable text first, because
// that is what gets chunked and what should decide whether two captures of
// the same URL are the same document. An MHTML archive embeds timestamps
// and request ids, so it hashes differently on every visit; raw HTML is
// steadier than that but still carries whatever the server varied per
// request, so it sits behind the readable text too.
var hashPreference = []string{ArtifactReadable, ArtifactMHTML, ArtifactHTML, ArtifactPDF, ArtifactScreenshot}

// dirTimeFormat is RFC3339 with the colons swapped for dashes: still
// sortable and still readable as a timestamp, but legal as a path segment
// on every platform (Windows forbids ':').
const dirTimeFormat = "2006-01-02T15-04-05Z"

// maxSlugLen bounds the URL-derived half of a directory name. Some
// filesystems cap a single path segment at 255 bytes; the timestamp,
// separator and any collision suffix have to fit alongside it.
const maxSlugLen = 60

// tmpPrefix marks the staging directory a capture is assembled in. The
// leading dot keeps it out of the way of an inbox watcher, which must skip
// dot-prefixed entries — the rename into place is what publishes a
// capture, and nothing before it is complete.
const tmpPrefix = ".tmp-capture-"

// spoolPrefix marks the directory a capture's chunks stream into before the
// envelope is assembled. Dot-prefixed for the same reason as tmpPrefix: an
// inbox watcher must skip both.
const spoolPrefix = ".spool-capture-"

// staleStagingAge is how old an abandoned staging or spool directory must
// be before a later capture cleans it up. A process killed mid-capture
// cannot remove its own, and the bytes it left behind are not small.
const staleStagingAge = 24 * time.Hour

// Writer writes envelopes into an inbox directory.
type Writer struct {
	// Inbox is the directory envelopes are created in. Empty means
	// DefaultInbox(), or — for an envelope naming a profile — that
	// profile's own inbox (see resolveInbox).
	Inbox string
	// Now supplies the current time; nil means time.Now.
	Now func() time.Time
}

func (w *Writer) inbox() string {
	if strings.TrimSpace(w.Inbox) != "" {
		return w.Inbox
	}
	return DefaultInbox()
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// resolveInbox decides where this envelope lands, and returns a warning to
// carry back to the caller when it could not land where it asked to.
//
// An envelope naming a profile goes into that profile's own inbox — that
// separation IS the feature, so it outranks the writer's configured inbox
// only when the writer was not pointed anywhere in particular. An explicit
// Inbox (`capture page --out`, a test) is a direct instruction about this
// one capture and wins; the profile is still recorded in meta.json.
//
// A profile id the profiles root would not accept (a separator, a '..', an
// empty string after trimming) is dropped rather than obeyed: the capture
// lands in the default inbox, unprofiled, with a warning. Losing the page
// over a bad id would be worse, and writing it wherever the id pointed
// would be worse still.
func (w *Writer) resolveInbox(meta *Meta) (string, string) {
	profile := strings.TrimSpace(meta.Profile)
	if profile == "" {
		return w.inbox(), ""
	}
	if !profiledir.ValidProfileID(profile) {
		meta.Profile = ""
		return w.inbox(), fmt.Sprintf("unusable profile id %q: saved to the default inbox instead", clampID(profile))
	}
	if strings.TrimSpace(w.Inbox) != "" {
		return w.Inbox, ""
	}
	dir, err := ProfileInbox(profile)
	if err != nil {
		meta.Profile = ""
		return w.inbox(), fmt.Sprintf("%v: saved to the default inbox instead", err)
	}
	return dir, ""
}

// Write stages the envelope in a temporary directory inside the inbox and
// renames it into place, so a watcher never sees a partially-written
// capture. On any failure the staging directory is removed and nothing is
// published.
func (w *Writer) Write(env *Envelope) (*Result, error) {
	if env == nil {
		return nil, errors.New("capture: nil envelope")
	}
	// Whatever happens below, the spooled chunks are consumed here.
	defer env.Cleanup()
	now := w.now()
	env.Meta.Normalize(now)
	if strings.TrimSpace(env.Meta.URL) == "" && strings.TrimSpace(env.Meta.CanonicalURL) == "" {
		return nil, errors.New("capture: meta has no url")
	}
	names, err := sortedArtifactNames(env.Artifacts)
	if err != nil {
		return nil, err
	}
	if env.Meta.ContentHash == "" {
		hash, err := contentHash(env.Artifacts, names)
		if err != nil {
			return nil, err
		}
		env.Meta.ContentHash = hash
	}

	inbox, warning := w.resolveInbox(&env.Meta)
	if warning != "" {
		env.Warnings = append(env.Warnings, warning)
	}
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		return nil, fmt.Errorf("create inbox %s: %w", inbox, err)
	}
	sweepStaleStaging(inbox, now)
	tmp, err := os.MkdirTemp(inbox, tmpPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := os.Chmod(tmp, 0o700); err != nil {
		return nil, fmt.Errorf("secure staging dir: %w", err)
	}

	var total int64
	for _, name := range names {
		artifact := env.Artifacts[name]
		if err := artifact.writeTo(filepath.Join(tmp, name)); err != nil {
			return nil, err
		}
		total += artifact.Size()
	}
	metaBlob, err := json.MarshalIndent(env.Meta, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode meta: %w", err)
	}
	metaBlob = append(metaBlob, '\n')
	if err := Inline(metaBlob).writeTo(filepath.Join(tmp, MetaFile)); err != nil {
		return nil, err
	}
	total += int64(len(metaBlob))

	dir, err := commit(tmp, inbox, env.Meta.capturedTime(now), env.Meta.DedupeURL())
	if err != nil {
		return nil, err
	}
	committed = true

	return &Result{
		Path:      dir,
		Meta:      env.Meta,
		Artifacts: names,
		Bytes:     total,
		Warnings:  env.Warnings,
	}, nil
}

// commit renames the staging directory to its final name, stepping past a
// name another capture of the same URL in the same second already took.
func commit(tmp, inbox string, at time.Time, rawURL string) (string, error) {
	base := at.UTC().Format(dirTimeFormat) + "-" + Slug(rawURL)
	for attempt := 0; attempt < 100; attempt++ {
		name := base
		if attempt > 0 {
			name = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		dir := filepath.Join(inbox, name)
		// Rename would happily move the staging dir *inside* an existing
		// directory of that name, so the collision has to be checked for
		// rather than discovered.
		if _, err := os.Lstat(dir); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("check %s: %w", dir, err)
		}
		if err := os.Rename(tmp, dir); err != nil {
			if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrNotExist) {
				continue // lost a race with a concurrent capture
			}
			return "", fmt.Errorf("publish capture to %s: %w", dir, err)
		}
		return dir, nil
	}
	return "", fmt.Errorf("capture: no free directory name for %s", base)
}

// sweepStaleStaging removes staging and spool directories left behind by a
// process that died mid-capture. Best effort and silent: a capture must
// never fail because someone else's leftovers could not be cleaned up.
func sweepStaleStaging(inbox string, now time.Time) {
	entries, err := os.ReadDir(inbox)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || (!strings.HasPrefix(name, tmpPrefix) && !strings.HasPrefix(name, spoolPrefix)) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < staleStagingAge {
			continue
		}
		_ = os.RemoveAll(filepath.Join(inbox, name))
	}
}

// sortedArtifactNames validates every artifact name and returns them in a
// stable order.
func sortedArtifactNames(artifacts map[string]Artifact) ([]string, error) {
	names := make([]string, 0, len(artifacts))
	for name := range artifacts {
		if !ValidArtifactName(name) {
			return nil, fmt.Errorf("capture: invalid artifact name %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// contentHash is the second half of the dedupe key. It hashes the first
// artifact present in hashPreference order, falling back to the first
// artifact alphabetically, and returns "" for an envelope with no bytes at
// all.
func contentHash(artifacts map[string]Artifact, sorted []string) (string, error) {
	pick := ""
	for _, name := range hashPreference {
		if _, ok := artifacts[name]; ok {
			pick = name
			break
		}
	}
	if pick == "" && len(sorted) > 0 {
		pick = sorted[0]
	}
	if pick == "" {
		return "", nil
	}
	hash, err := artifacts[pick].hash()
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", pick, err)
	}
	return hash, nil
}

// ValidArtifactName reports whether name is safe to use as a file name
// inside an envelope directory. The sender picks these names, so they are
// held to a strict whitelist: no separators, no traversal, no dotfiles, no
// surprises from a Windows reserved name.
func ValidArtifactName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return false
	}
	if name == MetaFile {
		return false // reserved: the writer owns meta.json
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	if strings.Contains(name, "..") {
		return false
	}
	stem, _, _ := strings.Cut(name, ".")
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4",
		"COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3",
		"LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return false
	}
	return true
}

// Slug turns a captured URL into the readable half of a directory name:
// host and path, lowercased, with everything outside [a-z0-9] collapsed to
// single dashes and the whole thing bounded. A URL that will not parse is
// still slugged, from its raw text.
func Slug(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		s = host + " " + strings.Trim(u.EscapedPath(), "/")
	}
	var b strings.Builder
	lastDash := true // leading dashes are trimmed by construction
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= maxSlugLen {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "capture"
	}
	return out
}
