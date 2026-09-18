// Package xlsx reads and writes the slice of the Office Open XML spreadsheet
// format that mono-agent needs: rows of cell values in, rows of cell values
// out. It is deliberately not a general spreadsheet library — there is no
// styling, no formulas, no charts, no streaming writer.
//
// The reason it exists rather than a dependency is robustness. A workflow can
// be pointed at any .xlsx on disk or fetched from anywhere, and a panic in a
// node takes down the daemon and every workflow running in it. So every index
// a cell record takes into the shared-string or style table is bounds-checked,
// every count and size read out of the file is treated as untrusted, and the
// package returns an error rather than panicking on malformed input. The caps
// in limits.go bound what a single file can cost.
//
// Reader output is matched to what github.com/xuri/excelize/v2 returned from
// File.GetRows, because existing workflows consume it. See doc comments on
// Rows and on formatDateTime for the places that match deliberately differs.
package xlsx

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

const (
	pathWorkbook      = "xl/workbook.xml"
	pathWorkbookRels  = "xl/_rels/workbook.xml.rels"
	pathSharedStrings = "xl/sharedStrings.xml"
	pathStyles        = "xl/styles.xml"

	relTypeWorksheet = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet"
)

// File is an opened workbook. It is not safe for concurrent use.
type File struct {
	zr    *zip.ReadCloser
	parts map[string]*zip.File

	// limit is the allowance this file started with, kept for the error
	// message so it names the budget that was actually applied.
	limit int64

	// budget is the shared decompression allowance for the whole file, so a
	// bomb split across many parts is caught just the same as one big part.
	budget int64

	sheets   []sheetRef
	date1904 bool

	sstLoaded bool
	sst       []string

	stylesLoaded bool
	cellXfs      []int          // style index -> numFmtId
	numFmts      map[int]string // custom numFmtId -> format code
}

type sheetRef struct {
	name string
	part string // zip path of the worksheet XML, "" if unresolved
}

// OpenFile opens the workbook at name.
func OpenFile(name string) (*File, error) {
	zr, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}
	f := &File{
		zr:     zr,
		parts:  make(map[string]*zip.File),
		budget: decompressionBudget,
		limit:  decompressionBudget,
	}
	if len(zr.File) > maxZipEntries {
		zr.Close()
		return nil, fmt.Errorf("xlsx: container has %d entries, limit is %d", len(zr.File), maxZipEntries)
	}
	for _, zf := range zr.File {
		// Normalise the few spellings a writer might use. Keep the first
		// entry for a name: a duplicate name is how a file smuggles a second
		// definition of a part past a reader that keeps the last one.
		p := strings.TrimPrefix(path.Clean("/"+zf.Name), "/")
		if _, dup := f.parts[p]; !dup {
			f.parts[p] = zf
		}
	}
	if err := f.readWorkbook(); err != nil {
		zr.Close()
		return nil, err
	}
	return f, nil
}

// Close releases the underlying file handle.
func (f *File) Close() error { return f.zr.Close() }

// SheetNames returns the worksheet names in workbook order.
func (f *File) SheetNames() []string {
	names := make([]string, 0, len(f.sheets))
	for _, s := range f.sheets {
		names = append(names, s.name)
	}
	return names
}

// open returns a reader over part, charged against the file's decompression
// budget. Missing parts are not an error to the caller: several are optional.
func (f *File) open(part string) (io.ReadCloser, bool, error) {
	zf, ok := f.parts[part]
	if !ok {
		return nil, false, nil
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, false, fmt.Errorf("xlsx: open %s: %w", part, err)
	}
	return &budgetReader{r: rc, c: rc, part: part, budget: &f.budget, limit: f.limit}, true, nil
}

// budgetReader draws every byte it yields from a shared allowance. The
// declared uncompressed size in the zip header is not trusted; only bytes that
// actually come out of the decompressor are counted.
type budgetReader struct {
	r      io.Reader
	c      io.Closer
	part   string
	budget *int64
	limit  int64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if *b.budget <= 0 {
		// Spent, but a part that ends exactly on the limit is legitimate, so
		// find out whether anything is actually left before refusing. The
		// probed byte is not lost: if there is one, this read fails anyway.
		var probe [1]byte
		if n, err := b.r.Read(probe[:]); n == 0 && err == io.EOF {
			return 0, io.EOF
		}
		return 0, fmt.Errorf("xlsx: %s: decompressed size exceeds %d bytes", b.part, b.limit)
	}
	if int64(len(p)) > *b.budget {
		p = p[:*b.budget]
	}
	n, err := b.r.Read(p)
	*b.budget -= int64(n)
	return n, err
}

func (b *budgetReader) Close() error { return b.c.Close() }

// newDecoder returns an XML decoder in strict mode. Strict mode is what makes
// an undefined entity an error instead of an expansion, which is the entity
// bomb defence; encoding/xml caps nesting depth on its own.
func newDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.Strict = true
	return d
}

// readWorkbook resolves sheet names to worksheet parts and reads the date
// system flag. Sheet order and part names are both untrusted: the rels table
// is the only correct way to get from a sheet name to its XML, since neither
// file order in the zip nor the sheetN.xml numbering has to match.
func (f *File) readWorkbook() error {
	rc, ok, err := f.open(pathWorkbook)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("xlsx: not a workbook: xl/workbook.xml is missing")
	}
	defer rc.Close()

	var wb struct {
		WorkbookPr struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets struct {
			Sheet []struct {
				Name string `xml:"name,attr"`
				RID  string `xml:"id,attr"` // r:id; matched by local name
			} `xml:"sheet"`
		} `xml:"sheets"`
	}
	if err := newDecoder(rc).Decode(&wb); err != nil {
		return fmt.Errorf("xlsx: parse %s: %w", pathWorkbook, err)
	}
	switch strings.ToLower(strings.TrimSpace(wb.WorkbookPr.Date1904)) {
	case "1", "true", "on":
		f.date1904 = true
	}

	rels, err := f.readRels()
	if err != nil {
		return err
	}
	for _, s := range wb.Sheets.Sheet {
		f.sheets = append(f.sheets, sheetRef{name: s.Name, part: rels[s.RID]})
	}
	return nil
}

// readRels maps relationship ids to worksheet part paths.
func (f *File) readRels() (map[string]string, error) {
	rc, ok, err := f.open(pathWorkbookRels)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]string{}, nil
	}
	defer rc.Close()

	var doc struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if err := newDecoder(rc).Decode(&doc); err != nil {
		return nil, fmt.Errorf("xlsx: parse %s: %w", pathWorkbookRels, err)
	}
	out := make(map[string]string, len(doc.Rel))
	for _, r := range doc.Rel {
		if r.Type != relTypeWorksheet || strings.EqualFold(r.Mode, "External") {
			continue
		}
		out[r.ID] = resolveTarget(r.Target)
	}
	return out, nil
}

// resolveTarget turns a relationship target into a zip path. Targets are
// relative to the part that owns the rels file (xl/), or absolute from the
// package root. path.Clean also neutralises any ".." traversal.
func resolveTarget(target string) string {
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(path.Clean(target), "/")
	}
	return strings.TrimPrefix(path.Clean("/xl/"+target), "/")
}

// sharedStrings reads xl/sharedStrings.xml on first use.
func (f *File) sharedStrings() ([]string, error) {
	if f.sstLoaded {
		return f.sst, nil
	}
	f.sstLoaded = true

	rc, ok, err := f.open(pathSharedStrings)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	defer rc.Close()

	// Streamed rather than decoded whole so the count attribute — which is
	// attacker-controlled and would otherwise size an allocation — is never
	// read at all, and so the cap applies as we go.
	d := newDecoder(rc)
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xlsx: parse %s: %w", pathSharedStrings, err)
		}
		se, isStart := tok.(xml.StartElement)
		if !isStart || se.Name.Local != "si" {
			continue
		}
		var si richText
		if err := d.DecodeElement(&si, &se); err != nil {
			return nil, fmt.Errorf("xlsx: parse %s: %w", pathSharedStrings, err)
		}
		if len(f.sst) >= maxSharedStrings {
			return nil, fmt.Errorf("xlsx: shared string table exceeds %d entries", maxSharedStrings)
		}
		f.sst = append(f.sst, si.String())
	}
	return f.sst, nil
}

// richText is the shape shared by <si> and <is>: either a single <t>, or a
// sequence of <r> runs each holding a <t>.
type richText struct {
	T string `xml:"t"`
	R []struct {
		T string `xml:"t"`
	} `xml:"r"`
}

func (rt *richText) String() string {
	if len(rt.R) == 0 {
		return rt.T
	}
	var b strings.Builder
	b.WriteString(rt.T)
	for _, r := range rt.R {
		b.WriteString(r.T)
	}
	return b.String()
}

// styles reads xl/styles.xml on first use, keeping only what cell formatting
// needs: the numFmtId of each cellXfs entry, and the custom format codes.
func (f *File) styles() error {
	if f.stylesLoaded {
		return nil
	}
	f.stylesLoaded = true
	f.numFmts = map[int]string{}

	rc, ok, err := f.open(pathStyles)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer rc.Close()

	// A token walk rather than a whole-document decode, because <xf> appears
	// under both cellStyleXfs and cellXfs and only the latter is what a cell's
	// s attribute indexes.
	d := newDecoder(rc)
	inCellXfs := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("xlsx: parse %s: %w", pathStyles, err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "cellXfs":
				inCellXfs = true
			case "xf":
				if !inCellXfs {
					continue
				}
				if len(f.cellXfs) >= maxCellXfs {
					return fmt.Errorf("xlsx: cellXfs exceeds %d entries", maxCellXfs)
				}
				id, _ := attrInt(se.Attr, "numFmtId")
				f.cellXfs = append(f.cellXfs, id)
			case "numFmt":
				if len(f.numFmts) >= maxNumFmts {
					return fmt.Errorf("xlsx: numFmts exceeds %d entries", maxNumFmts)
				}
				id, ok := attrInt(se.Attr, "numFmtId")
				if !ok {
					continue
				}
				// formatCode16 is the newer spelling and wins where both are
				// present, matching how Excel itself resolves them.
				code := attrString(se.Attr, "formatCode16")
				if code == "" {
					code = attrString(se.Attr, "formatCode")
				}
				f.numFmts[id] = code
			}
		case xml.EndElement:
			if se.Name.Local == "cellXfs" {
				inCellXfs = false
			}
		}
	}
	return nil
}

func attrString(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func attrInt(attrs []xml.Attr, local string) (int, bool) {
	v := strings.TrimSpace(attrString(attrs, local))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Rows returns every row of the named sheet as a slice of cell values.
//
// The shape matches what excelize's File.GetRows produced, because workflows
// already depend on it:
//
//   - Each row runs up to its own last non-empty cell, so rows are ragged;
//     they are not padded out to the width of the sheet.
//   - A cell reference may be missing, and then the cell is "" — a row that
//     declares only C3 comes back as ["", "", <C3>].
//   - A row may be missing entirely, and then an empty row holds its place so
//     that row N of the sheet stays at index N-1.
//   - Trailing empty rows are dropped.
//
// An unknown sheet name is an error.
func (f *File) Rows(sheet string) ([][]string, error) {
	part := ""
	found := false
	for _, s := range f.sheets {
		if s.name == sheet {
			part, found = s.part, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("sheet %s does not exist", sheet)
	}
	if part == "" {
		return nil, fmt.Errorf("xlsx: sheet %s has no worksheet part", sheet)
	}
	rc, ok, err := f.open(part)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("xlsx: sheet %s: missing part %s", sheet, part)
	}
	defer rc.Close()

	if err := f.styles(); err != nil {
		return nil, err
	}

	d := newDecoder(rc)
	var out [][]string
	rowNum := 0
	cells := 0
	inSheetData := false

	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xlsx: sheet %s: %w", sheet, err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "sheetData":
				inSheetData = true
			case "row":
				if !inSheetData {
					continue
				}
				rowNum++
				if r, ok := attrInt(se.Attr, "r"); ok && r > rowNum {
					// Trust r only to skip forward. A row numbered at or
					// before one already emitted would otherwise let a file
					// rewrite earlier output or drive the gap fill negative;
					// such a row is appended in document order instead.
					rowNum = r
				}
				if rowNum > maxRows {
					return nil, fmt.Errorf("xlsx: sheet %s: row %d exceeds the %d row limit", sheet, rowNum, maxRows)
				}
				row, err := f.readRow(d, &se, &cells)
				if err != nil {
					return nil, fmt.Errorf("xlsx: sheet %s: row %d: %w", sheet, rowNum, err)
				}
				if len(row) == 0 {
					continue
				}
				for len(out) < rowNum-1 {
					out = append(out, nil)
				}
				out = append(out, row)
			}
		case xml.EndElement:
			if se.Name.Local == "sheetData" {
				// Nothing after the cells is of interest, and walking it
				// would mean decoding the rest of the part for nothing.
				return out, nil
			}
		}
	}
	return out, nil
}

// rawCell is a <c> record. Every attribute is kept as a string so that a
// malformed value is a value we reject, not a decode error for the whole file.
type rawCell struct {
	R  string    `xml:"r,attr"`
	S  string    `xml:"s,attr"`
	T  string    `xml:"t,attr"`
	V  *string   `xml:"v"`
	IS *richText `xml:"is"`
	F  *struct {
		Text string `xml:",chardata"`
	} `xml:"f"`
}

// readRow decodes the cells of one <row>, returning them positioned by column
// and truncated at the last non-empty cell.
func (f *File) readRow(d *xml.Decoder, start *xml.StartElement, cells *int) ([]string, error) {
	var row []string
	col := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return row, nil
		}
		if err != nil {
			return nil, err
		}
		switch se := tok.(type) {
		case xml.StartElement:
			if se.Name.Local != "c" {
				// Skip whole subtrees such as <extLst> rather than walking
				// into them, so nothing inside can be mistaken for a cell or
				// for the end of the row.
				if err := d.Skip(); err != nil {
					return nil, err
				}
				continue
			}
			col++
			var c rawCell
			if err := d.DecodeElement(&c, &se); err != nil {
				return nil, err
			}
			if c.R != "" {
				n, err := columnOf(c.R)
				if err != nil {
					return nil, err
				}
				col = n
			}
			if col > maxColumns {
				return nil, fmt.Errorf("column %d exceeds the %d column limit", col, maxColumns)
			}
			val, err := f.cellValue(&c)
			if err != nil {
				return nil, err
			}
			// An empty value contributes nothing unless the cell carries a
			// formula, which is how excelize decided a cell existed; that in
			// turn is what makes rows ragged rather than sheet-wide.
			if val == "" && c.F == nil {
				continue
			}
			// Count the padding as well as the value, so a sheet that places
			// one cell far to the right on every row is bounded too. A cell
			// left of one already seen adds no padding.
			pad := 0
			if col-1 > len(row) {
				pad = col - 1 - len(row)
			}
			*cells += pad + 1
			if *cells > maxCells {
				return nil, fmt.Errorf("sheet exceeds the %d cell limit", maxCells)
			}
			for len(row) < col-1 {
				row = append(row, "")
			}
			row = append(row, val)
		case xml.EndElement:
			if se.Name.Local == start.Name.Local {
				return row, nil
			}
		}
	}
}

// columnOf extracts the 1-based column index from a cell reference like "AB12".
func columnOf(ref string) (int, error) {
	col := 0
	for i := 0; i < len(ref); i++ {
		ch := ref[i]
		switch {
		case ch >= 'A' && ch <= 'Z':
			col = col*26 + int(ch-'A') + 1
		case ch >= 'a' && ch <= 'z':
			col = col*26 + int(ch-'a') + 1
		case ch >= '0' && ch <= '9':
			if col == 0 {
				return 0, fmt.Errorf("invalid cell reference %q", ref)
			}
			return col, nil
		default:
			return 0, fmt.Errorf("invalid cell reference %q", ref)
		}
		if col > maxColumns {
			return 0, fmt.Errorf("invalid cell reference %q: column out of range", ref)
		}
	}
	if col == 0 {
		return 0, fmt.Errorf("invalid cell reference %q", ref)
	}
	return col, nil
}

// cellValue renders one cell to the string a workflow sees.
func (f *File) cellValue(c *rawCell) (string, error) {
	v := ""
	if c.V != nil {
		v = *c.V
	}
	switch c.T {
	case "b":
		// Booleans render as Excel displays them, not as the stored 0/1.
		if strings.TrimSpace(v) == "1" {
			return "TRUE", nil
		}
		return "FALSE", nil

	case "s":
		if v == "" {
			return f.formatted(c.S, "", false), nil
		}
		idx, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return "", fmt.Errorf("invalid shared string index %q", v)
		}
		sst, err := f.sharedStrings()
		if err != nil {
			return "", err
		}
		// The whole reason this package exists: excelize indexed here without
		// a lower-bound check, so a negative index panicked (GO-2026-6452).
		if idx < 0 || idx >= len(sst) {
			return "", fmt.Errorf("invalid shared string index %d", idx)
		}
		return f.formatted(c.S, sst[idx], false), nil

	case "str":
		// A formula's cached string result is shown as stored, never
		// number-formatted.
		return v, nil

	case "inlineStr":
		if c.IS != nil {
			return f.formatted(c.S, c.IS.String(), false), nil
		}
		return f.formatted(c.S, v, false), nil

	default:
		// Numbers, dates and error values all live here.
		return f.formatted(c.S, v, true), nil
	}
}

// formatted applies the cell's number format. numeric says whether the value
// should first be normalised as a number — true for numeric cells, false for
// text, which is never reinterpreted as a number even under a date format.
//
// Every failure to resolve a style or a format code falls back to the raw
// value rather than erroring: an unreadable format is a display detail, and a
// file with a broken style table still has readable data.
func (f *File) formatted(sAttr, v string, numeric bool) string {
	if numeric {
		v = normalizeNumber(v)
	}
	if sAttr == "" || sAttr == "0" {
		return v
	}
	s, err := strconv.Atoi(strings.TrimSpace(sAttr))
	if err != nil || s < 0 || s >= len(f.cellXfs) {
		return v
	}
	numFmtID := f.cellXfs[s]
	code, ok := f.numFmts[numFmtID]
	if !ok {
		code, ok = builtInNumFmt[numFmtID]
		if !ok {
			return v
		}
	}
	if out, ok := formatDateTime(v, code, f.date1904); ok {
		return out
	}
	return v
}

// normalizeNumber renders a stored number the way Excel displays it under the
// General format: trailing zeros dropped ("1.50" -> "1.5", "3.0" -> "3"), and
// anything beyond 15 significant digits shown in scientific notation, which is
// the precision Excel itself keeps. Values that are not numbers — including
// ones padded with spaces — pass through untouched.
func normalizeNumber(v string) string {
	if v == "" || strings.Contains(v, "_") {
		return v
	}
	fl, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return v
	}
	plain := strconv.FormatFloat(fl, 'f', -1, 64)
	if len(plain)-strings.Count(plain, ".") > 15 {
		return strconv.FormatFloat(fl, 'G', 15, 64)
	}
	return plain
}
