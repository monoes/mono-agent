package captureexport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// seed writes captures into a fresh inbox and returns it.
func seed(t *testing.T, metas ...capture.Meta) string {
	t.Helper()
	inbox := filepath.Join(t.TempDir(), "inbox")
	for _, meta := range metas {
		w := &capture.Writer{Inbox: inbox}
		res, err := w.Write(&capture.Envelope{
			Meta: meta,
			Artifacts: map[string]capture.Artifact{
				capture.ArtifactMHTML:    capture.Inline([]byte("archive of " + meta.URL)),
				capture.ArtifactReadable: capture.Inline([]byte("# " + meta.Title + "\n\nbody\n")),
			},
		})
		if err != nil {
			t.Fatalf("seed %s: %v", meta.URL, err)
		}
		_ = res
	}
	return inbox
}

func strptr(s string) *string { return &s }

func exportToBytes(t *testing.T, opts ExportOptions) ([]byte, *Manifest) {
	t.Helper()
	var buf bytes.Buffer
	man, err := Export(&buf, opts)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	return buf.Bytes(), man
}

// readArchive returns every member of the archive as path -> bytes.
func readArchive(t *testing.T, blob []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	out := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = body
	}
	return out
}

func TestExportArchiveLayout(t *testing.T) {
	inbox := seed(t,
		capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"},
		capture.Meta{URL: "https://example.com/b", Title: "B", CapturedAt: "2026-09-21T10:00:00Z"},
	)
	blob, man := exportToBytes(t, ExportOptions{Inbox: inbox, Host: "testhost"})
	members := readArchive(t, blob)

	if _, ok := members[ManifestPath]; !ok {
		t.Fatalf("archive has no %s, members: %v", ManifestPath, keys(members))
	}
	if readme, ok := members[ReadmePath]; !ok || !strings.Contains(string(readme), "tar -xzf") {
		t.Errorf("archive should carry a README explaining how to read it without the tool")
	}
	if man.Count != 2 || len(man.Captures) != 2 {
		t.Fatalf("manifest counted %d captures", man.Count)
	}
	if man.Format != Format || man.Version != Version {
		t.Errorf("manifest = %s v%d", man.Format, man.Version)
	}
	if man.Source.Inbox != inbox || man.Source.Host != "testhost" {
		t.Errorf("manifest source = %+v", man.Source)
	}

	for _, row := range man.Captures {
		if !strings.HasPrefix(row.Dir, CapturesDir+"/") {
			t.Errorf("capture dir %q should sit under %s/", row.Dir, CapturesDir)
		}
		names := make([]string, 0, len(row.Files))
		for _, f := range row.Files {
			names = append(names, f.Name)
			body, ok := members[path.Join(row.Dir, f.Name)]
			if !ok {
				t.Fatalf("manifest lists %s/%s but the archive does not hold it", row.Dir, f.Name)
			}
			if int64(len(body)) != f.Size {
				t.Errorf("%s: manifest size %d, archive holds %d", f.Name, f.Size, len(body))
			}
			sum := sha256.Sum256(body)
			if hex.EncodeToString(sum[:]) != f.SHA256 {
				t.Errorf("%s: manifest checksum does not match the bytes", f.Name)
			}
		}
		sort.Strings(names)
		want := []string{capture.MetaFile, capture.ArtifactMHTML, capture.ArtifactReadable}
		sort.Strings(want)
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("capture holds %v, want %v", names, want)
		}
	}
}

func TestExportManifestIsReadableOnItsOwn(t *testing.T) {
	inbox := seed(t, capture.Meta{
		URL: "https://example.com/a", CanonicalURL: "https://example.com/a",
		Title: "A", CapturedAt: "2026-09-20T10:00:00Z",
		Collection: strptr("research"), Tags: []string{"go", "web"}, Source: "crawl",
	})
	blob, _ := exportToBytes(t, ExportOptions{Inbox: inbox})
	var man Manifest
	if err := json.Unmarshal(readArchive(t, blob)[ManifestPath], &man); err != nil {
		t.Fatalf("manifest.json does not parse on its own: %v", err)
	}
	row := man.Captures[0]
	if row.Collection != "research" || row.CaptureSrc != "crawl" || strings.Join(row.Tags, ",") != "go,web" {
		t.Errorf("manifest row lost provenance: %+v", row)
	}
	if row.ContentHash == "" || row.CapturedAt == "" || row.Title != "A" {
		t.Errorf("manifest row = %+v", row)
	}
}

func TestExportFilters(t *testing.T) {
	inbox := seed(t,
		capture.Meta{URL: "https://example.com/old", Title: "Old", CapturedAt: "2026-09-01T10:00:00Z"},
		capture.Meta{URL: "https://example.com/new", Title: "New", CapturedAt: "2026-09-20T10:00:00Z", Collection: strptr("research")},
		capture.Meta{URL: "https://example.com/other", Title: "Other", CapturedAt: "2026-09-21T10:00:00Z", Collection: strptr("misc")},
	)

	since, err := ParseSince("2026-09-15")
	if err != nil {
		t.Fatalf("ParseSince: %v", err)
	}
	_, man := exportToBytes(t, ExportOptions{Inbox: inbox, Since: since, SinceText: "2026-09-15"})
	if man.Count != 2 {
		t.Errorf("--since kept %d captures, want 2", man.Count)
	}
	if man.Filters == nil || man.Filters.Since != "2026-09-15" {
		t.Errorf("the manifest should record the filter, got %+v", man.Filters)
	}

	_, man = exportToBytes(t, ExportOptions{Inbox: inbox, Collection: "Research"})
	if man.Count != 1 || man.Captures[0].Title != "New" {
		t.Errorf("--collection kept %d captures: %+v", man.Count, man.Captures)
	}

	_, man = exportToBytes(t, ExportOptions{Inbox: inbox, Since: since, Collection: "misc"})
	if man.Count != 1 || man.Captures[0].Title != "Other" {
		t.Errorf("combined filters kept %+v", man.Captures)
	}
}

func TestExportEmptyInbox(t *testing.T) {
	_, man := exportToBytes(t, ExportOptions{Inbox: filepath.Join(t.TempDir(), "nothing")})
	if man.Count != 0 || len(man.Captures) != 0 {
		t.Errorf("an empty inbox should produce an empty archive, got %+v", man)
	}
}

func TestParseSince(t *testing.T) {
	if got, err := ParseSince(""); err != nil || !got.IsZero() {
		t.Errorf("empty --since should mean no filter, got %v %v", got, err)
	}
	if _, err := ParseSince("last tuesday"); err == nil {
		t.Error("an unparseable date should be refused")
	}
	got, err := ParseSince("2026-09-15T08:30:00Z")
	if err != nil || !got.Equal(time.Date(2026, 9, 15, 8, 30, 0, 0, time.UTC)) {
		t.Errorf("ParseSince(RFC3339) = %v, %v", got, err)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var _ = os.ReadDir
