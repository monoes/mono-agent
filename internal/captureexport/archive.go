// Package captureexport moves captures between machines as a single
// portable file (GLU-08).
//
// The formats inside an envelope — MHTML, PDF, Markdown, JSON — were chosen
// because they outlive the tool that wrote them, and an archive that hid
// them inside a bespoke container would throw that away. So the archive is
// a plain gzipped tar: every capture is its own directory of the same files
// the inbox holds, a manifest.json at the root says what is in it, and a
// README.txt at the root explains the layout to someone who has never heard
// of monoagent. `tar -xzf` is the whole import procedure if this tool is
// not around.
package captureexport

import "time"

// Format and Version identify the archive. A reader that does not
// understand Version refuses rather than guessing.
const (
	Format  = "monoagent.capture.archive"
	Version = 1
)

// The fixed paths inside the archive.
const (
	ManifestPath = "manifest.json"
	ReadmePath   = "README.txt"
	CapturesDir  = "captures"
)

// Manifest is the archive's table of contents. It is written first so that
// `tar -xzOf archive.tar.gz manifest.json` answers "what is in here?"
// without unpacking the whole thing.
type Manifest struct {
	Format    string        `json:"format"`
	Version   int           `json:"version"`
	CreatedAt string        `json:"createdAt"`
	Tool      string        `json:"tool"`
	Source    Source        `json:"source"`
	Filters   *Filters      `json:"filters,omitempty"`
	Count     int           `json:"count"`
	Bytes     int64         `json:"bytes"`
	Captures  []ManifestRow `json:"captures"`
}

// Source records where the archive was made, which is provenance a reader
// months later will want and cannot reconstruct.
type Source struct {
	Inbox string `json:"inbox"`
	Host  string `json:"host,omitempty"`
}

// Filters records what the export selected, so an archive that is missing a
// capture says why rather than looking incomplete.
type Filters struct {
	Since      string `json:"since,omitempty"`
	Collection string `json:"collection,omitempty"`
}

// ManifestRow is one capture: where it sits in the archive, the provenance
// worth indexing without opening each meta.json, and a checksum per file so
// the archive can be verified with sha256sum alone.
type ManifestRow struct {
	Dir          string    `json:"dir"`
	URL          string    `json:"url"`
	CanonicalURL string    `json:"canonicalUrl,omitempty"`
	Title        string    `json:"title,omitempty"`
	CapturedAt   string    `json:"capturedAt,omitempty"`
	ContentHash  string    `json:"contentHash,omitempty"`
	Collection   string    `json:"collection,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	CaptureSrc   string    `json:"source,omitempty"`
	Bytes        int64     `json:"bytes"`
	Files        []FileRow `json:"files"`
}

// FileRow is one file inside one capture directory.
type FileRow struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Note is one capture an export or import did not take, and why.
type Note struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// parseSince accepts the two date forms a person actually types: a full
// RFC3339 timestamp, or a plain calendar day, which means midnight UTC.
func parseSince(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errBadDate(s)
}
