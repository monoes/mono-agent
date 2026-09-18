package xlsx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures in these tests are assembled here rather than committed as
// binaries, so that what a test exercises is readable in the test.

// book describes a workbook to assemble part by part.
type book struct {
	// sheets lists worksheets in workbook order. Each entry's part is the zip
	// path its XML is written to, which need not be sheet1.xml and need not
	// match workbook order.
	sheets []bookSheet
	// sharedStrings are the <si> bodies of xl/sharedStrings.xml. A nil slice
	// omits the part entirely.
	sharedStrings []string
	// numFmts are <numFmt> elements and cellXfs are <xf> elements, written
	// verbatim into xl/styles.xml. A nil cellXfs omits the styles part.
	numFmts  []string
	cellXfs  []string
	date1904 bool
	// extra and override write or replace arbitrary parts, for malformed cases.
	extra map[string]string
	omit  []string
}

type bookSheet struct {
	name string
	part string // default "xl/worksheets/sheetN.xml"
	rows string // <row> elements
}

const sheetNS = `http://schemas.openxmlformats.org/spreadsheetml/2006/main`

func (b book) parts() map[string]string {
	var sheetTags, relTags strings.Builder
	parts := map[string]string{}

	for i, s := range b.sheets {
		part := s.part
		if part == "" {
			part = fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		}
		rid := fmt.Sprintf("rId%d", i+1)
		fmt.Fprintf(&sheetTags, `<sheet name="%s" sheetId="%d" r:id="%s"/>`, s.name, i+1, rid)
		fmt.Fprintf(&relTags, `<Relationship Id="%s" Type="%s" Target="%s"/>`,
			rid, relTypeWorksheet, strings.TrimPrefix(part, "xl/"))
		parts[part] = xmlHeader + `<worksheet xmlns="` + sheetNS + `"><sheetData>` + s.rows + `</sheetData></worksheet>`
	}

	pr := ""
	if b.date1904 {
		pr = `<workbookPr date1904="1"/>`
	}
	parts["[Content_Types].xml"] = contentTypesXML
	parts["_rels/.rels"] = rootRelsXML
	parts[pathWorkbook] = xmlHeader + `<workbook xmlns="` + sheetNS +
		`" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		pr + `<sheets>` + sheetTags.String() + `</sheets></workbook>`
	parts[pathWorkbookRels] = xmlHeader +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		relTags.String() + `</Relationships>`

	if b.sharedStrings != nil {
		parts[pathSharedStrings] = xmlHeader + `<sst xmlns="` + sheetNS + `" count="` +
			fmt.Sprint(len(b.sharedStrings)) + `">` + strings.Join(b.sharedStrings, "") + `</sst>`
	}
	if b.cellXfs != nil {
		nf := ""
		if len(b.numFmts) > 0 {
			nf = `<numFmts count="` + fmt.Sprint(len(b.numFmts)) + `">` + strings.Join(b.numFmts, "") + `</numFmts>`
		}
		parts[pathStyles] = xmlHeader + `<styleSheet xmlns="` + sheetNS + `">` + nf +
			`<cellXfs count="` + fmt.Sprint(len(b.cellXfs)) + `">` + strings.Join(b.cellXfs, "") + `</cellXfs></styleSheet>`
	}
	for name, body := range b.extra {
		parts[name] = body
	}
	for _, name := range b.omit {
		delete(parts, name)
	}
	return parts
}

// write assembles the workbook and returns the path it was written to.
func (b book) write(t *testing.T) string {
	t.Helper()
	return writeZip(t, b.parts())
}

func writeZip(t *testing.T, parts map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return writeTemp(t, buf.Bytes())
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.xlsx")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// readRows opens path and reads one sheet, failing the test on error.
func readRows(t *testing.T, path, sheet string) [][]string {
	t.Helper()
	f, err := OpenFile(path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	rows, err := f.Rows(sheet)
	if err != nil {
		t.Fatalf("Rows(%q): %v", sheet, err)
	}
	return rows
}

// rowsErr opens path and reads one sheet, expecting an error rather than a
// panic. Any panic is reported as a test failure, since a panic in this
// package is the failure mode the whole package exists to prevent.
func rowsErr(t *testing.T, path, sheet string) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on malformed input: %v", r)
		}
	}()
	f, openErr := OpenFile(path)
	if openErr != nil {
		return openErr
	}
	defer f.Close()
	_, err = f.Rows(sheet)
	return err
}

func equalRows(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// writeZipWithDuplicate writes parts, then appends a second entry under name
// carrying different content.
func writeZipWithDuplicate(t *testing.T, parts map[string]string, name, second string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(n, body string) {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	write(name, parts[name])
	for n, body := range parts {
		if n != name {
			write(n, body)
		}
	}
	write(name, second)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return writeTemp(t, buf.Bytes())
}
