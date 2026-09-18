package xlsx

import (
	"fmt"
	"strings"
	"testing"
)

// TestBuiltInDateFormats pins the rendering of the built-in date and time ids.
//
// Every expectation here was captured from excelize v2.11.0 reading the same
// bytes, so this table is what the node used to return. The four cells where
// this package deliberately departs are in TestDivergesFromExcelize.
func TestBuiltInDateFormats(t *testing.T) {
	ids := []int{14, 15, 16, 17, 18, 19, 20, 21, 22, 45, 46, 47}

	tests := []struct {
		serial string
		want   []string // one per id, in the order above
	}{
		{"0", []string{"01-00-00", "0-Jan-00", "0-Jan", "Jan-00", "12:00 AM", "12:00:00 AM",
			"00:00", "00:00:00", "1/0/00 00:00", "00:00", "0:00:00", "00:00.0"}},
		{"1", []string{"01-01-00", "1-Jan-00", "1-Jan", "Jan-00", "12:00 AM", "12:00:00 AM",
			"00:00", "00:00:00", "1/1/00 00:00", "00:00", "24:00:00", "00:00.0"}},
		// Serial 59 is the last real day before Excel's phantom leap day.
		{"59", []string{"02-28-00", "28-Feb-00", "28-Feb", "Feb-00", "12:00 AM", "12:00:00 AM",
			"00:00", "00:00:00", "2/28/00 00:00", "00:00", "1416:00:00", "00:00.0"}},
		// Serial 60 is 1900-02-29, a day that never existed.
		{"60", []string{"02-29-00", "29-Feb-00", "29-Feb", "Feb-00", "12:00 AM", "12:00:00 AM",
			"00:00", "00:00:00", "2/29/00 00:00", "00:00", "1440:00:00", "00:00.0"}},
		{"61", []string{"03-01-00", "1-Mar-00", "1-Mar", "Mar-00", "12:00 AM", "12:00:00 AM",
			"00:00", "00:00:00", "3/1/00 00:00", "00:00", "1464:00:00", "00:00.0"}},
		{"25569", []string{"01-01-70", "1-Jan-70", "1-Jan", "Jan-70", "12:00 AM", "12:00:00 AM",
			"00:00", "00:00:00", "1/1/70 00:00", "00:00", "613656:00:00", "00:00.0"}},
		{"45000", []string{"03-15-23", "15-Mar-23", "15-Mar", "Mar-23", "12:00 AM", "12:00:00 AM",
			"00:00", "00:00:00", "3/15/23 00:00", "00:00", "1080000:00:00", "00:00.0"}},
		{"45000.5", []string{"03-15-23", "15-Mar-23", "15-Mar", "Mar-23", "12:00 PM", "12:00:00 PM",
			"12:00", "12:00:00", "3/15/23 12:00", "00:00", "1080012:00:00", "00:00.0"}},
		{"45000.75", []string{"03-15-23", "15-Mar-23", "15-Mar", "Mar-23", "6:00 PM", "6:00:00 PM",
			"18:00", "18:00:00", "3/15/23 18:00", "00:00", "1080018:00:00", "00:00.0"}},
		{"45123.123456", []string{"07-16-23", "16-Jul-23", "16-Jul", "Jul-23", "2:57 AM", "2:57:47 AM",
			"02:57", "02:57:47", "7/16/23 02:57", "57:47", "1082954:57:47", "57:46.6"}},
		{"0.25", []string{"01-00-00", "0-Jan-00", "0-Jan", "Jan-00", "6:00 AM", "6:00:00 AM",
			"06:00", "06:00:00", "1/0/00 06:00", "00:00", "6:00:00", "00:00.0"}},
		// A value a hair under midnight rounds up into the next day.
		{"44927.999999", []string{"01-02-23", "2-Jan-23", "2-Jan", "Jan-23", "12:00 AM", "12:00:00 AM",
			"00:00", "00:00:00", "1/2/23 00:00", "00:00", "1078272:00:00", "59:59.9"}},
		// A negative serial is not a date; the raw number stands.
		{"-1", []string{"-1", "-1", "-1", "-1", "-1", "-1", "-1", "-1", "-1", "-1", "-1", "-1"}},
		// Text under a date format is shown as the text it is.
		{"not a number", []string{"not a number", "not a number", "not a number", "not a number",
			"not a number", "not a number", "not a number", "not a number", "not a number",
			"not a number", "not a number", "not a number"}},
	}

	var cellXfs []string
	cellXfs = append(cellXfs, `<xf numFmtId="0"/>`)
	for _, id := range ids {
		cellXfs = append(cellXfs, fmt.Sprintf(`<xf numFmtId="%d"/>`, id))
	}

	for _, tc := range tests {
		t.Run(tc.serial, func(t *testing.T) {
			var row strings.Builder
			row.WriteString(`<row r="1">`)
			for i := range ids {
				fmt.Fprintf(&row, `<c r="%s1" s="%d"><v>%s</v></c>`, columnName(i+1), i+1, tc.serial)
			}
			row.WriteString(`</row>`)

			b := book{cellXfs: cellXfs, sheets: []bookSheet{{name: "S", rows: row.String()}}}
			got := readRows(t, b.write(t), "S")
			if len(got) != 1 {
				t.Fatalf("rows = %q, want one row", got)
			}
			for i, id := range ids {
				if got[0][i] != tc.want[i] {
					t.Errorf("numFmtId %d: got %q, want %q", id, got[0][i], tc.want[i])
				}
			}
		})
	}
}

// TestDate1904 covers the alternate date system. Excel's 1904 system has no
// phantom leap day, and serial 0 is 1904-01-01.
//
// These expectations follow Excel, not excelize: excelize converted serials at
// or below 61 through a different calendar than the rest, so in a 1904 workbook
// it rendered serial 1 as 1900-01-03 and serial 59 as 1-Mar-04.
func TestDate1904(t *testing.T) {
	tests := []struct {
		serial string
		want   string // under "yyyy-mm-dd"
	}{
		{"0", "1904-01-01"},
		{"1", "1904-01-02"},
		{"58", "1904-02-28"},
		{"59", "1904-02-29"}, // 1904 really was a leap year
		{"60", "1904-03-01"},
		{"1461", "1908-01-01"}, // four years on, one of them a leap year
		// The two systems are 1462 serials apart, so the same day is serial
		// 45000 in a 1900 workbook and serial 43538 in a 1904 one.
		{"43538", "2023-03-15"},
	}

	for _, tc := range tests {
		t.Run(tc.serial, func(t *testing.T) {
			b := book{
				date1904: true,
				cellXfs:  []string{`<xf numFmtId="0"/>`, `<xf numFmtId="164"/>`},
				numFmts:  []string{`<numFmt numFmtId="164" formatCode="yyyy-mm-dd"/>`},
				sheets:   []bookSheet{{name: "S", rows: `<row r="1"><c r="A1" s="1"><v>` + tc.serial + `</v></c></row>`}},
			}
			got := readRows(t, b.write(t), "S")
			if len(got) != 1 || got[0][0] != tc.want {
				t.Errorf("serial %s = %q, want %q", tc.serial, got, tc.want)
			}
		})
	}

	// The same serial means different days in the two systems.
	t.Run("offset from the 1900 system", func(t *testing.T) {
		for _, d1904 := range []bool{false, true} {
			b := book{
				date1904: d1904,
				cellXfs:  []string{`<xf numFmtId="0"/>`, `<xf numFmtId="164"/>`},
				numFmts:  []string{`<numFmt numFmtId="164" formatCode="yyyy-mm-dd"/>`},
				sheets:   []bookSheet{{name: "S", rows: `<row r="1"><c r="A1" s="1"><v>45000</v></c></row>`}},
			}
			got := readRows(t, b.write(t), "S")
			want := "2023-03-15"
			if d1904 {
				want = "2027-03-16"
			}
			if got[0][0] != want {
				t.Errorf("date1904=%v: got %q, want %q", d1904, got[0][0], want)
			}
		}
	})
}

// TestCustomDateFormats covers the custom format codes that real files use —
// Excel writes almost every date format as a custom numFmt with an id of 164
// or above, so the built-in table alone would leave most dates unformatted.
//
// Each expectation was checked against excelize v2.11.0 on the same bytes.
func TestCustomDateFormats(t *testing.T) {
	tests := []struct {
		code   string
		serial string
		want   string
	}{
		{`yyyy-mm-dd`, "45000", "2023-03-15"},
		{`yyyy"-"mm"-"dd`, "45000", "2023-03-15"},
		{`yyyy\-mm\-dd`, "45000", "2023-03-15"},
		{`dd/mm/yyyy`, "45000", "15/03/2023"},
		{`m/d/yyyy`, "45000", "3/15/2023"},
		{`mmmm d, yyyy`, "45000", "March 15, 2023"},
		{`d mmm yyyy`, "45000", "15 Mar 2023"},
		{`mmm d`, "45000", "Mar 15"},
		{`mmmmm`, "45000", "M"},
		{`dddd`, "45000", "Wednesday"},
		{`ddd, mmm d, yyyy`, "45000", "Wed, Mar 15, 2023"},
		{`yy/mm/dd`, "45000", "23/03/15"},
		{`yyyy-mm-dd hh:mm:ss`, "45000.5", "2023-03-15 12:00:00"},
		{`hh:mm:ss`, "45000.5", "12:00:00"},
		{`h:mm`, "45000.75", "18:00"},
		{`h:mm:ss AM/PM`, "45000.75", "6:00:00 PM"},
		{`h:mm A/P`, "45000.75", "6:00 P"},
		{`[h]:mm`, "45000.5", "1080012:00"},

		// The m/mm ambiguity: minutes next to an hour or a second field,
		// months everywhere else.
		{`mm-dd-yy`, "45123.123456", "07-16-23"},
		{`hh:mm`, "45123.123456", "02:57"},
		{`mm:ss`, "45123.123456", "57:47"},
		{`mmm`, "45123.123456", "Jul"},

		// Before 1900-03-01 Excel's weekday cycle runs one day behind the real
		// calendar, because the phantom leap day is part of the cycle.
		{`dddd`, "1", "Sunday"}, // 1900-01-01 was really a Monday
		{`dddd`, "60", "Wednesday"},
		{`dddd`, "61", "Thursday"},

		// Codes that are not dates leave the number alone.
		{`#,##0.00`, "45000", "45000"},
		{`0%`, "45000", "45000"},
		{`0.00E+00`, "45000", "45000"},
		{`@`, "45000", "45000"},
		{`_("$"* #,##0.00_)`, "45000", "45000"},
		{`[Red]0.0;[Blue]-0.0`, "45000", "45000"},
		{`General`, "45000", "45000"},
		{`[>100]yyyy;yyyy`, "45000", "45000"}, // conditional sections are not evaluated
		{``, "45000", "45000"},

		// Colour and locale prefixes do not stop a date from rendering.
		{`[Red]yyyy-mm-dd`, "45000", "2023-03-15"},
		{`[$-409]yyyy-mm-dd`, "45000", "2023-03-15"},

		// A serial past the end of Excel's calendar keeps its number.
		{`yyyy-mm-dd`, "2958466", "2958466"},
		{`yyyy-mm-dd`, "2958465", "9999-12-31"},
	}

	for _, tc := range tests {
		t.Run(tc.code+" "+tc.serial, func(t *testing.T) {
			b := book{
				cellXfs: []string{`<xf numFmtId="0"/>`, `<xf numFmtId="164"/>`},
				numFmts: []string{`<numFmt numFmtId="164" formatCode="` + escape(tc.code) + `"/>`},
				sheets:  []bookSheet{{name: "S", rows: `<row r="1"><c r="A1" s="1"><v>` + tc.serial + `</v></c></row>`}},
			}
			got := readRows(t, b.write(t), "S")
			if len(got) != 1 || len(got[0]) != 1 || got[0][0] != tc.want {
				t.Errorf("code %q serial %s = %q, want %q", tc.code, tc.serial, got, tc.want)
			}
		})
	}
}

// TestDivergesFromExcelize records, as executable documentation, the places
// where this package deliberately returns something other than what excelize
// v2.11.0 returned for the same bytes. Each is a case where excelize
// contradicted Excel.
func TestDivergesFromExcelize(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		serial   string
		date1904 bool
		excelize string
		want     string
		why      string
	}{
		{
			name:     "elapsed hours are not offset by the epoch",
			code:     "[h]:mm:ss",
			serial:   "0",
			date1904: true,
			excelize: "35088:00:00",
			want:     "0:00:00",
			why:      "elapsed time is a duration, measured from zero, not from the epoch",
		},
		{
			name:     "elapsed hours are not capped at a Duration",
			code:     "[h]:mm:ss",
			serial:   "2958465",
			excelize: "2562047:00:00",
			want:     "71003160:00:00",
			why:      "excelize saturated at the largest time.Duration rather than counting hours",
		},
		{
			name:     "sub-second digits come from the value",
			code:     "mm:ss.0",
			serial:   "45123.123456",
			excelize: "57:47.0",
			want:     "57:46.6",
			why:      "excelize rounded away the fraction and then printed a zero for it",
		},
		{
			name:     "sub-second digits do not round into the next day",
			code:     "mm:ss.0",
			serial:   "44927.999999",
			excelize: "00:00.0",
			want:     "59:59.9",
			why:      "a format showing tenths should not first round to a whole second",
		},
		{
			name:     "a year past 9999 does not overflow the field",
			code:     "mm-dd-yy",
			serial:   "2958465",
			date1904: true,
			excelize: "01-01-004",
			want:     "2958465",
			why:      "the serial is past the end of the 1904 calendar, so there is no date to show",
		},
		{
			name:     "the 1904 system has no phantom leap day",
			code:     "yyyy-mm-dd",
			serial:   "59",
			date1904: true,
			excelize: "1904-03-01",
			want:     "1904-02-29",
			why:      "1904 was a real leap year; only the 1900 system carries the phantom day",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := book{
				date1904: tc.date1904,
				cellXfs:  []string{`<xf numFmtId="0"/>`, `<xf numFmtId="164"/>`},
				numFmts:  []string{`<numFmt numFmtId="164" formatCode="` + escape(tc.code) + `"/>`},
				sheets:   []bookSheet{{name: "S", rows: `<row r="1"><c r="A1" s="1"><v>` + tc.serial + `</v></c></row>`}},
			}
			got := readRows(t, b.write(t), "S")
			if len(got) != 1 || got[0][0] != tc.want {
				t.Errorf("got %q, want %q (excelize returned %q; %s)", got, tc.want, tc.excelize, tc.why)
			}
		})
	}
}

// TestNumFmtParserRejectsGarbage checks that no format code, however strange,
// gets the parser to misbehave rather than decline.
func TestNumFmtParserRejectsGarbage(t *testing.T) {
	codes := []string{
		`"unterminated`, `\`, `[`, `[unclosed`, `_`, `*`,
		`[Blue]`, `[]`, `[$]`, `AM`, `A`, `A/`, `.`, `.x`,
		strings.Repeat("y", 500), strings.Repeat("[h]", 200),
		"\x00\x01", "yyyy�mm",
	}
	for _, code := range codes {
		t.Run(fmt.Sprintf("%q", code), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parseNumFmt(%q) panicked: %v", code, r)
				}
			}()
			toks, kind, ok := parseNumFmt(code)
			if ok && kind != fmtNotDate {
				// Accepting is fine as long as rendering also stays safe.
				_ = render(toks, 1, 0, 0, 1900, 1, 1, 0)
			}
		})
	}
}
