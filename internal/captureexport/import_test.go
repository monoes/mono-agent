package captureexport

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// roundTrip exports an inbox and imports it into another one.
func roundTrip(t *testing.T, src string, opts ImportOptions) *ImportResult {
	t.Helper()
	blob, _ := exportToBytes(t, ExportOptions{Inbox: src})
	res, err := Import(bytes.NewReader(blob), opts)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return res
}

func TestRoundTripRestoresEveryByte(t *testing.T) {
	src := seed(t,
		capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z",
			Collection: strptr("research"), Tags: []string{"go"}},
		capture.Meta{URL: "https://example.com/b", Title: "B", CapturedAt: "2026-09-21T10:00:00Z"},
	)
	dst := filepath.Join(t.TempDir(), "inbox")
	res := roundTrip(t, src, ImportOptions{Inbox: dst})

	if len(res.Imported) != 2 {
		t.Fatalf("imported %d captures, want 2 (%+v)", len(res.Imported), res)
	}
	if res.Manifest == nil || res.Manifest.Count != 2 {
		t.Errorf("import should report the archive's manifest, got %+v", res.Manifest)
	}

	before, err := capture.List(src)
	if err != nil {
		t.Fatal(err)
	}
	after, err := capture.List(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("inbox held %d captures, restored %d", len(before), len(after))
	}
	for i := range before {
		if before[i].URL != after[i].URL || before[i].Title != after[i].Title {
			t.Errorf("capture %d: %+v restored as %+v", i, before[i], after[i])
		}
		if filepath.Base(before[i].Path) != filepath.Base(after[i].Path) {
			t.Errorf("directory name changed: %s -> %s", before[i].Path, after[i].Path)
		}
		compareDirs(t, before[i].Path, after[i].Path)
	}
}

// compareDirs asserts two envelope directories hold the same files, byte
// for byte.
func compareDirs(t *testing.T, a, b string) {
	t.Helper()
	entries, err := os.ReadDir(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		want, err := os.ReadFile(filepath.Join(a, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(b, e.Name()))
		if err != nil {
			t.Fatalf("restored capture is missing %s: %v", e.Name(), err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s differs after the round trip", e.Name())
		}
	}
}

func TestImportCollisionSkipIsTheDefault(t *testing.T) {
	src := seed(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})
	dst := filepath.Join(t.TempDir(), "inbox")
	roundTrip(t, src, ImportOptions{Inbox: dst})

	// Edit the restored copy, then import the same archive again.
	entries, _ := capture.List(dst)
	marker := filepath.Join(entries[0].Path, capture.ArtifactReadable)
	if err := os.WriteFile(marker, []byte("edited locally"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := roundTrip(t, src, ImportOptions{Inbox: dst})
	if len(res.Imported) != 0 {
		t.Errorf("a second import should import nothing, got %+v", res.Imported)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "already in the inbox") {
		t.Errorf("the skip should be reported, got %+v", res.Skipped)
	}
	if body, _ := os.ReadFile(marker); string(body) != "edited locally" {
		t.Errorf("skip must not touch the existing capture, file now %q", body)
	}
	if after, _ := capture.List(dst); len(after) != 1 {
		t.Errorf("inbox holds %d captures, want 1", len(after))
	}
}

func TestImportCollisionRenameKeepsBoth(t *testing.T) {
	src := seed(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})
	dst := filepath.Join(t.TempDir(), "inbox")
	first := roundTrip(t, src, ImportOptions{Inbox: dst})
	second := roundTrip(t, src, ImportOptions{Inbox: dst, OnCollision: CollisionRename})

	if len(second.Imported) != 1 || !second.Imported[0].Renamed {
		t.Fatalf("rename should have imported a renamed copy, got %+v", second.Imported)
	}
	if second.Imported[0].Path == first.Imported[0].Path {
		t.Errorf("renamed capture reused the original path %s", second.Imported[0].Path)
	}
	if !strings.HasSuffix(second.Imported[0].Path, "-2") {
		t.Errorf("renamed path = %s, want a -2 suffix", second.Imported[0].Path)
	}
	if after, _ := capture.List(dst); len(after) != 2 {
		t.Errorf("inbox holds %d captures, want both", len(after))
	}
}

func TestImportCollisionOverwriteReplaces(t *testing.T) {
	src := seed(t, capture.Meta{URL: "https://example.com/a", Title: "A", CapturedAt: "2026-09-20T10:00:00Z"})
	dst := filepath.Join(t.TempDir(), "inbox")
	roundTrip(t, src, ImportOptions{Inbox: dst})

	entries, _ := capture.List(dst)
	marker := filepath.Join(entries[0].Path, capture.ArtifactReadable)
	if err := os.WriteFile(marker, []byte("edited locally"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file the archive does not hold must not survive the replacement.
	stray := filepath.Join(entries[0].Path, "stray.txt")
	if err := os.WriteFile(stray, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := roundTrip(t, src, ImportOptions{Inbox: dst, OnCollision: CollisionOverwrite})
	if len(res.Imported) != 1 || !res.Imported[0].Replaced {
		t.Fatalf("overwrite should report a replacement, got %+v", res.Imported)
	}
	if body, _ := os.ReadFile(marker); string(body) == "edited locally" {
		t.Error("overwrite left the old file in place")
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("overwrite should replace the directory, not merge into it")
	}
	if after, _ := capture.List(dst); len(after) != 1 {
		t.Errorf("inbox holds %d captures, want 1", len(after))
	}
}

func TestImportRejectsForeignArchives(t *testing.T) {
	blob := buildArchive(t, func(tw *tar.Writer) {
		if err := writeFileEntry(tw, "some/other/file.txt", []byte("hello"), zeroTime()); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := Import(bytes.NewReader(blob), ImportOptions{Inbox: t.TempDir()}); err == nil {
		t.Error("an archive that is not a capture archive should be refused")
	}
	if _, err := Import(strings.NewReader("not gzip at all"), ImportOptions{Inbox: t.TempDir()}); err == nil {
		t.Error("a non-archive should be refused")
	}
}

func TestImportRefusesFutureVersions(t *testing.T) {
	man, _ := json.Marshal(Manifest{Format: Format, Version: Version + 1})
	blob := buildArchive(t, func(tw *tar.Writer) {
		if err := writeFileEntry(tw, ManifestPath, man, zeroTime()); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := Import(bytes.NewReader(blob), ImportOptions{Inbox: t.TempDir()}); err == nil {
		t.Error("an archive from a newer version should be refused, not guessed at")
	}
}

// An archive arrived from somewhere else by definition: a path that climbs
// out of the inbox, a dot-prefixed staging name, or a symlink must never be
// unpacked.
//
// Every hostile entry here sits beside a legitimate capture, which must
// still land — otherwise "nothing escaped" would also be true of an import
// that silently did nothing at all, and the test could not tell them
// apart.
func TestImportRefusesUnsafePaths(t *testing.T) {
	man, _ := json.Marshal(Manifest{Format: Format, Version: Version})
	const meta = `{"url":"https://x.test/","title":"Good"}`
	blob := buildArchive(t, func(tw *tar.Writer) {
		for _, e := range []struct {
			name string
			body string
		}{
			{ManifestPath, string(man)},
			{"captures/2026-09-20T10-00-00Z-good/meta.json", meta}, // the control
			// path.Clean folds this one away before the importer sees it,
			// which is exactly why it is not the only case here.
			{"captures/../../escape/meta.json", meta},
			// A backslash is one character on this machine and a
			// separator on Windows, so Clean leaves it alone: this is the
			// entry the name check has to catch.
			{`captures/..\..\escape/meta.json`, meta},
			{"captures/.hidden/meta.json", meta},
		} {
			if err := writeFileEntry(tw, e.name, []byte(e.body), zeroTime()); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeSymlink, Name: "captures/ok/link", Linkname: "/etc/passwd",
			ModTime: zeroTime(), Format: tar.FormatPAX,
		}); err != nil {
			t.Fatal(err)
		}
	})

	root := t.TempDir()
	dst := filepath.Join(root, "nest", "inbox")
	res, err := Import(bytes.NewReader(blob), ImportOptions{Inbox: dst})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// The control landed; nothing else did.
	if len(res.Imported) != 1 || res.Imported[0].Name != "2026-09-20T10-00-00Z-good" {
		t.Fatalf("imported %+v, want only the safe capture", res.Imported)
	}
	if _, err := os.Stat(filepath.Join(res.Imported[0].Path, capture.MetaFile)); err != nil {
		t.Errorf("the safe capture did not land whole: %v", err)
	}

	// "escape" appears nowhere: not in the inbox, not beside it, not above
	// it. A `captures/../../escape` lands one directory up if the join is
	// naive, so that is the directory to look in.
	for _, dir := range []string{dst, filepath.Dir(dst), root, filepath.Dir(root)} {
		if _, err := os.Lstat(filepath.Join(dir, "escape")); err == nil {
			t.Errorf("a traversing path escaped into %s", dir)
		}
	}
	if _, err := os.Lstat(filepath.Join(dst, "ok", "link")); err == nil {
		t.Error("a symlink was unpacked")
	}
	if _, err := os.Lstat(filepath.Join(dst, "ok")); err == nil {
		t.Error("a symlink's directory was created")
	}
	if _, err := os.Lstat(filepath.Join(dst, ".hidden")); err == nil {
		t.Error("a dot-prefixed capture name was unpacked")
	}
	// And what was refused is reported, by name.
	reported := map[string]string{}
	for _, note := range res.Skipped {
		reported[note.Name] = note.Reason
	}
	for _, name := range []string{".hidden", `..\..\escape`} {
		if reason, ok := reported[name]; !ok {
			t.Errorf("%q was dropped without a word: %+v", name, res.Skipped)
		} else if !strings.Contains(reason, "unsafe") {
			t.Errorf("%q was skipped for %q", name, reason)
		}
	}
	// Only the inbox exists under the root, holding only the control.
	left, err := os.ReadDir(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Name() != "2026-09-20T10-00-00Z-good" {
		names := make([]string, 0, len(left))
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Errorf("the inbox holds %v", names)
	}
}

func TestImportSkipsCapturesWithoutMeta(t *testing.T) {
	man, _ := json.Marshal(Manifest{Format: Format, Version: Version})
	blob := buildArchive(t, func(tw *tar.Writer) {
		if err := writeFileEntry(tw, ManifestPath, man, zeroTime()); err != nil {
			t.Fatal(err)
		}
		if err := writeFileEntry(tw, "captures/2026-09-20T10-00-00Z-x/readable.md", []byte("# orphan"), zeroTime()); err != nil {
			t.Fatal(err)
		}
	})
	dst := filepath.Join(t.TempDir(), "inbox")
	res, err := Import(bytes.NewReader(blob), ImportOptions{Inbox: dst})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Imported) != 0 {
		t.Errorf("a directory with no meta.json is not a capture: %+v", res.Imported)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, capture.MetaFile) {
		t.Errorf("skipped = %+v", res.Skipped)
	}
	left, _ := os.ReadDir(dst)
	for _, e := range left {
		t.Errorf("a rejected capture left %s behind", e.Name())
	}
}

func TestParseCollision(t *testing.T) {
	for in, want := range map[string]Collision{
		"":            CollisionSkip,
		"skip":        CollisionSkip,
		"RENAME":      CollisionRename,
		" overwrite ": CollisionOverwrite,
	} {
		got, err := ParseCollision(in)
		if err != nil || got != want {
			t.Errorf("ParseCollision(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseCollision("merge"); err == nil {
		t.Error("an unknown collision mode should be refused")
	}
}

// buildArchive writes a hand-made gzipped tar, for the cases an Export
// would never produce.
func buildArchive(t *testing.T, fill func(*tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	fill(tw)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// zeroTime keeps the hand-built archives deterministic.
func zeroTime() time.Time { return time.Unix(0, 0).UTC() }
