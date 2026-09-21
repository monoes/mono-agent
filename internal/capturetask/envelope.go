package capturetask

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/capture"
)

// Reading the capture a task is about. The board half of this package
// (task.go) decides where an issue goes; this half decides what it says —
// the title, the provenance block in the body, and the attachment per file
// that makes the task carry the capture rather than only mention it.

// envelope is a capture directory, read once.
type envelope struct {
	dir   string
	meta  capture.Meta
	files []os.FileInfo
}

// readEnvelope loads a capture directory, refusing anything that is not one.
//
// The path is resolved through any symlinks first, because it does not
// stay here: it is written onto a board that monomind's dashboard and
// other agents read, and a reader opens it months later. A link records
// where a capture will be decided to have been, rather than where it is.
//
// The path is not required to sit inside an inbox. `capture task
// ./capture` is a documented way to file a capture that was never in one,
// and a containment rule that has to be argued with is worse than the
// absolute path it would replace — what this does instead is make sure the
// path written down is the real one, and that it is a capture at all.
func readEnvelope(dir string) (*envelope, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("capturetask: no capture path given")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("capturetask: %s is not a capture — a capture is a directory holding %s", abs, capture.MetaFile)
	}
	meta, err := capture.ReadMeta(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("capturetask: %s is not a capture — it has no %s", abs, capture.MetaFile)
		}
		return nil, fmt.Errorf("capturetask: %s has an unreadable %s: %w", abs, capture.MetaFile, err)
	}
	env := &envelope{dir: abs, meta: *meta}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read capture %s: %w", abs, err)
	}
	for _, e := range entries {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil {
			env.files = append(env.files, info)
		}
	}
	sort.Slice(env.files, func(i, j int) bool { return env.files[i].Name() < env.files[j].Name() })
	return env, nil
}

// attachments lists every file of the capture, so the task carries the
// capture rather than only mentioning it.
func (e *envelope) attachments(ts string) []Attachment {
	out := make([]Attachment, 0, len(e.files))
	for _, f := range e.files {
		out = append(out, Attachment{
			Type:      attachmentType(f.Name()),
			Name:      f.Name(),
			Path:      filepath.Join(e.dir, f.Name()),
			SizeBytes: f.Size(),
			AddedAt:   ts,
		})
	}
	return out
}

// attachmentType names the kind of artifact, for a reader listing them.
func attachmentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mhtml", ".html", ".htm":
		return "archive"
	case ".pdf":
		return "pdf"
	case ".md":
		return "markdown"
	case ".png", ".jpg", ".jpeg", ".webp":
		return "image"
	case ".json":
		return "metadata"
	case ".csv":
		return "table"
	}
	return "file"
}

func (e *envelope) ref() *CaptureRef {
	ref := &CaptureRef{
		Path:         e.dir,
		URL:          e.meta.URL,
		CanonicalURL: e.meta.CanonicalURL,
		Title:        e.meta.Title,
		CapturedAt:   e.meta.CapturedAt,
		ContentHash:  e.meta.ContentHash,
		Source:       e.meta.Source,
		Tags:         e.meta.Tags,
	}
	if e.meta.Collection != nil {
		ref.Collection = strings.TrimSpace(*e.meta.Collection)
	}
	return ref
}

// describe builds the issue body: what the person said, then where the
// capture came from — a block a reader can act on without this tool.
func describe(note string, e *envelope) string {
	var b strings.Builder
	if s := strings.TrimSpace(note); s != "" {
		b.WriteString(s)
		b.WriteString("\n\n")
	}
	b.WriteString("## Capture\n\n")
	if url := e.meta.DedupeURL(); url != "" {
		fmt.Fprintf(&b, "- Source: %s\n", url)
	}
	if e.meta.Title != "" {
		fmt.Fprintf(&b, "- Title: %s\n", e.meta.Title)
	}
	if e.meta.Byline != nil && strings.TrimSpace(*e.meta.Byline) != "" {
		fmt.Fprintf(&b, "- Byline: %s\n", strings.TrimSpace(*e.meta.Byline))
	}
	if e.meta.CapturedAt != "" {
		fmt.Fprintf(&b, "- Captured: %s", e.meta.CapturedAt)
		if e.meta.Source != "" {
			fmt.Fprintf(&b, " (%s)", e.meta.Source)
		}
		b.WriteString("\n")
	}
	if len(e.meta.Tags) > 0 {
		fmt.Fprintf(&b, "- Tags: %s\n", strings.Join(e.meta.Tags, ", "))
	}
	fmt.Fprintf(&b, "- Envelope: %s\n", e.dir)
	names := make([]string, 0, len(e.files))
	for _, f := range e.files {
		names = append(names, f.Name())
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, "- Files: %s\n", strings.Join(names, ", "))
	}
	if note := e.meta.Note; note != nil && strings.TrimSpace(*note) != "" {
		fmt.Fprintf(&b, "\n> %s\n", strings.TrimSpace(*note))
	}
	return b.String()
}
