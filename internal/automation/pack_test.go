package automation

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zent struct {
	name string
	body string
	mode fs.FileMode
}

func makeZip(t *testing.T, ents []zent) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestValidatePackageClean(t *testing.T) {
	p, err := OpenDir(acmeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	p.Source = SourceImported
	is := Validate(p)
	if HasErrors(is) {
		t.Fatalf("clean package has errors: %+v", errorsOnly(is))
	}
	var sawScripts bool
	for _, i := range is {
		if i.Code == "contains_scripts" {
			sawScripts = true
		}
	}
	if !sawScripts {
		t.Error("contains_scripts warning missing")
	}
}

func TestValidatePackageProblems(t *testing.T) {
	files := acmeFiles()
	delete(files, "actions/create_contact.json")  // listed, missing
	files["actions/orphan.json"] = `{"steps":[]}` // present, unlisted
	files["scripts/extra.js"] = "return 1"        // undeclared
	files["selectors.json"] = `{"a":{"candidates":[{"css":"x","text":"y"}]},"b":{"candidates":[]}}`
	files["fragments/broken.json"] = `{not json`
	delete(files, "icon.svg")
	p, err := OpenDir(writeTree(t, t.TempDir(), files))
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, i := range Validate(p) {
		codes[i.Code] = true
	}
	for _, want := range []string{"missing_action_file", "unlisted_action", "undeclared_script",
		"bad_selector_candidate", "empty_selector", "bad_fragment_json", "missing_icon"} {
		if !codes[want] {
			t.Errorf("missing issue %s; got %v", want, codes)
		}
	}
}

func TestPackDeterministicAndOpenFile(t *testing.T) {
	dir := acmeDir(t)
	var a, b bytes.Buffer
	if err := Pack(dir, &a); err != nil {
		t.Fatal(err)
	}
	// Touch mtimes: output must not change.
	os.Chtimes(filepath.Join(dir, "selectors.json"), zipEpoch.AddDate(3, 0, 0), zipEpoch.AddDate(3, 0, 0))
	if err := Pack(dir, &b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("Pack is not deterministic")
	}
	zr, _ := zip.NewReader(bytes.NewReader(a.Bytes()), int64(a.Len()))
	if zr.File[0].Name != ChecksumsFile {
		t.Errorf("first entry %s, want CHECKSUMS", zr.File[0].Name)
	}
	p, err := OpenFile(packDir(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.ID != "acme-crm" || p.Source != SourceImported {
		t.Errorf("OpenFile: %+v", p.Manifest)
	}
	if _, err := fs.Stat(p.FS, ChecksumsFile); err == nil {
		t.Error("CHECKSUMS should not be part of the package FS")
	}
	if def, err := p.Action("list_deals"); err != nil || len(def.Steps) != 4 {
		t.Errorf("Action: %v %v", def, err)
	}
}

func TestZipSafety(t *testing.T) {
	manifest := acmeFiles()["automation.json"]
	good := func(extra ...zent) []zent { return append([]zent{{name: "automation.json", body: manifest}}, extra...) }
	sum := func(body string) string { return sha256Hex([]byte(body)) }

	big := strings.Repeat("x", 2048)
	cases := map[string]struct {
		ents  []zent
		setup func() func()
	}{
		"zip-slip":       {ents: good(zent{name: "../evil.json", body: "{}"})},
		"nested-slip":    {ents: good(zent{name: "actions/../../evil", body: "{}"})},
		"absolute":       {ents: good(zent{name: "/etc/passwd", body: "x"})},
		"windows-abs":    {ents: good(zent{name: "C:/evil", body: "x"})},
		"backslash":      {ents: good(zent{name: `actions\..\..\evil`, body: "x"})},
		"symlink":        {ents: good(zent{name: "link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777})},
		"duplicate":      {ents: good(zent{name: "automation.json", body: manifest})},
		"bad-checksums":  {ents: good(zent{name: "CHECKSUMS", body: fmt.Sprintf("%s  automation.json\n", sum("other"))})},
		"unlisted-file":  {ents: good(zent{name: "x.txt", body: "x"}, zent{name: "CHECKSUMS", body: fmt.Sprintf("%s  automation.json\n", sum(manifest))})},
		"listed-missing": {ents: good(zent{name: "CHECKSUMS", body: fmt.Sprintf("%s  automation.json\n%s  gone.txt\n", sum(manifest), sum("x"))})},
		"file-too-big": {ents: good(zent{name: "big.txt", body: big}), setup: func() func() {
			old := MaxFileBytes
			MaxFileBytes = 1024
			return func() { MaxFileBytes = old }
		}},
		"too-many-files": {ents: good(zent{name: "a", body: "1"}, zent{name: "b", body: "2"}), setup: func() func() {
			old := MaxFiles
			MaxFiles = 2
			return func() { MaxFiles = old }
		}},
		"archive-too-big": {ents: good(zent{name: "big.txt", body: big}), setup: func() func() {
			old := MaxArchiveBytes
			MaxArchiveBytes = 512
			return func() { MaxArchiveBytes = old }
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.setup != nil {
				defer c.setup()()
			}
			_, err := readZip(bytes.NewReader(makeZip(t, c.ents)))
			if err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if !errors.Is(err, ErrUnsafeArchive) {
				t.Errorf("error %v does not wrap ErrUnsafeArchive", err)
			}
		})
	}

	// A correct CHECKSUMS passes.
	ok := good(zent{name: "CHECKSUMS", body: fmt.Sprintf("%s  automation.json\n", sum(manifest))})
	if _, err := readZip(bytes.NewReader(makeZip(t, ok))); err != nil {
		t.Errorf("valid archive rejected: %v", err)
	}
}

func TestSnapshotDirRejectsSymlink(t *testing.T) {
	dir := acmeDir(t)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "scripts", "leak.js")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if _, err := snapshotDir(dir); !errors.Is(err, ErrUnsafeArchive) {
		t.Errorf("symlink in dir accepted: %v", err)
	}
}
