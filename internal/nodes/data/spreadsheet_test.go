package data

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

func runSpreadsheet(t *testing.T, items []workflow.Item, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	t.Helper()
	node := &SpreadsheetNode{}
	return node.Execute(context.Background(), workflow.NodeInput{Items: items}, config)
}

// TestSpreadsheetXLSXRoundTrip covers the two XLSX operations together, since
// what write_xlsx produces is what read_xlsx is expected to consume.
func TestSpreadsheetXLSXRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.xlsx")
	items := []workflow.Item{
		workflow.NewItem(map[string]interface{}{"name": "Ada", "role": "engineer"}),
		workflow.NewItem(map[string]interface{}{"name": "Grace", "role": "admiral"}),
	}

	out, err := runSpreadsheet(t, items, map[string]interface{}{
		"operation": "write_xlsx", "file_path": path, "sheet_name": "People",
	})
	if err != nil {
		t.Fatalf("write_xlsx: %v", err)
	}
	if got := out[0].Items[0].JSON["rows_written"]; got != 2 {
		t.Errorf("rows_written = %v, want 2", got)
	}

	out, err = runSpreadsheet(t, nil, map[string]interface{}{
		"operation": "read_xlsx", "file_path": path, "sheet_name": "People",
	})
	if err != nil {
		t.Fatalf("read_xlsx: %v", err)
	}
	got := out[0].Items
	if len(got) != 2 {
		t.Fatalf("read back %d items, want 2", len(got))
	}
	if got[0].JSON["name"] != "Ada" || got[0].JSON["role"] != "engineer" {
		t.Errorf("first item = %v", got[0].JSON)
	}
	if got[1].JSON["name"] != "Grace" || got[1].JSON["role"] != "admiral" {
		t.Errorf("second item = %v", got[1].JSON)
	}
}

// TestSpreadsheetXLSXWithoutHeader checks the has_header=false path, where
// fields are named by column index.
func TestSpreadsheetXLSXWithoutHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.xlsx")
	items := []workflow.Item{
		workflow.NewItem(map[string]interface{}{"a": "1", "b": "2"}),
	}
	if _, err := runSpreadsheet(t, items, map[string]interface{}{
		"operation": "write_xlsx", "file_path": path, "sheet_name": "S", "has_header": false,
	}); err != nil {
		t.Fatalf("write_xlsx: %v", err)
	}

	out, err := runSpreadsheet(t, nil, map[string]interface{}{
		"operation": "read_xlsx", "file_path": path, "sheet_name": "S", "has_header": false,
	})
	if err != nil {
		t.Fatalf("read_xlsx: %v", err)
	}
	if len(out[0].Items) != 1 {
		t.Fatalf("got %d items, want 1", len(out[0].Items))
	}
	json := out[0].Items[0].JSON
	if json["0"] != "1" || json["1"] != "2" {
		t.Errorf("item = %v, want columns keyed by index", json)
	}
}

// TestSpreadsheetSheetNameConfigKeys checks that both config spellings still
// select the sheet, and that the default is Sheet1.
func TestSpreadsheetSheetNameConfigKeys(t *testing.T) {
	for _, key := range []string{"sheet_name", "sheet"} {
		t.Run(key, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "out.xlsx")
			cfg := map[string]interface{}{"operation": "write_xlsx", "file_path": path, key: "Chosen"}
			if _, err := runSpreadsheet(t, []workflow.Item{
				workflow.NewItem(map[string]interface{}{"x": "1"}),
			}, cfg); err != nil {
				t.Fatalf("write_xlsx: %v", err)
			}
			if _, err := runSpreadsheet(t, nil, map[string]interface{}{
				"operation": "read_xlsx", "file_path": path, key: "Chosen",
			}); err != nil {
				t.Fatalf("read_xlsx: %v", err)
			}
		})
	}

	t.Run("defaults to Sheet1", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.xlsx")
		if _, err := runSpreadsheet(t, []workflow.Item{
			workflow.NewItem(map[string]interface{}{"x": "1"}),
		}, map[string]interface{}{"operation": "write_xlsx", "file_path": path}); err != nil {
			t.Fatalf("write_xlsx: %v", err)
		}
		if _, err := runSpreadsheet(t, nil, map[string]interface{}{
			"operation": "read_xlsx", "file_path": path, "sheet_name": "Sheet1",
		}); err != nil {
			t.Fatalf("read_xlsx on the default sheet: %v", err)
		}
	})
}

// TestSpreadsheetErrorShapes pins the wording of the errors a workflow author
// sees, which did not change with the reader behind them.
func TestSpreadsheetErrorShapes(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.xlsx")
	if _, err := runSpreadsheet(t, []workflow.Item{
		workflow.NewItem(map[string]interface{}{"x": "1"}),
	}, map[string]interface{}{"operation": "write_xlsx", "file_path": good, "sheet_name": "Real"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tests := []struct {
		name   string
		config map[string]interface{}
		want   []string
	}{
		{
			name:   "unknown operation",
			config: map[string]interface{}{"operation": "frobnicate"},
			want:   []string{`data.spreadsheet: unknown operation "frobnicate"`},
		},
		{
			name: "missing file",
			config: map[string]interface{}{
				"operation": "read_xlsx", "file_path": filepath.Join(dir, "nope.xlsx"), "sheet_name": "S",
			},
			want: []string{"data.spreadsheet read_xlsx: open", "nope.xlsx"},
		},
		{
			name: "unknown sheet",
			config: map[string]interface{}{
				"operation": "read_xlsx", "file_path": good, "sheet_name": "Missing",
			},
			want: []string{
				`data.spreadsheet read_xlsx: get rows from sheet "Missing"`,
				"sheet Missing does not exist",
			},
		},
		{
			name: "unwritable destination",
			config: map[string]interface{}{
				"operation": "write_xlsx", "file_path": filepath.Join(dir, "no-such-dir", "x.xlsx"),
			},
			want: []string{"data.spreadsheet write_xlsx: save"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runSpreadsheet(t, []workflow.Item{
				workflow.NewItem(map[string]interface{}{"x": "1"}),
			}, tc.config)
			if err == nil {
				t.Fatalf("got no error, want one containing %q", tc.want)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestSpreadsheetReadXLSXDoesNotPanicOnMalformedFile is the node-level form of
// the bug this change was made for: a crafted workbook must fail the node, not
// the process that is running it.
func TestSpreadsheetReadXLSXDoesNotPanicOnMalformedFile(t *testing.T) {
	dir := t.TempDir()

	// A worksheet whose only cell points at a negative shared-string index,
	// which is the input that panicked inside excelize (GO-2026-6452).
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/></Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="S" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="1"><si><t>only</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<sheetData><row r="1"><c r="A1" t="s"><v>-1</v></c></row></sheetData></worksheet>`,
	}

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

	path := filepath.Join(dir, "hostile.xlsx")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("read_xlsx panicked on a malformed file: %v", r)
		}
	}()
	_, err := runSpreadsheet(t, nil, map[string]interface{}{
		"operation": "read_xlsx", "file_path": path, "sheet_name": "S",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid shared string index -1") {
		t.Fatalf("error = %v, want an invalid shared string index error", err)
	}
}

// TestSpreadsheetCSVStillWorks guards the CSV paths, which this change did not
// touch but which share the row and item conversion helpers.
func TestSpreadsheetCSVStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	items := []workflow.Item{
		workflow.NewItem(map[string]interface{}{"name": "Ada", "n": 1}),
	}
	if _, err := runSpreadsheet(t, items, map[string]interface{}{
		"operation": "write_csv", "file_path": path,
	}); err != nil {
		t.Fatalf("write_csv: %v", err)
	}
	out, err := runSpreadsheet(t, nil, map[string]interface{}{
		"operation": "read_csv", "file_path": path,
	})
	if err != nil {
		t.Fatalf("read_csv: %v", err)
	}
	if len(out[0].Items) != 1 || out[0].Items[0].JSON["name"] != "Ada" || out[0].Items[0].JSON["n"] != "1" {
		t.Errorf("items = %v", out[0].Items)
	}
}
