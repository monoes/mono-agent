package xlsx

// Resource caps applied while reading a workbook. Everything in an .xlsx is
// attacker-controlled once a workflow reads a file it did not create, so each
// cap exists to bound the work a single malformed or hostile file can cause.
// They are generous enough that no spreadsheet a human would actually open in
// Excel runs into them, and finite enough that a crafted file cannot exhaust
// memory or wedge the daemon.
const (
	// maxDecompressedBytes bounds the total number of bytes we will inflate
	// out of the zip container across every part we read. Guards zip bombs,
	// where a few kilobytes of deflate stream expand to gigabytes.
	maxDecompressedBytes = 512 << 20 // 512 MiB

	// maxZipEntries bounds how many members of the container we will index.
	// A real workbook has a handful per sheet.
	maxZipEntries = 8192

	// maxRows and maxColumns mirror the grid limits of Excel itself, so any
	// file that stays inside them is one Excel could have produced. They cap
	// both the rows we emit and the gap-filling done for missing rows.
	maxRows    = 1048576
	maxColumns = 16384

	// maxCells bounds the total number of cell values a single sheet may
	// produce, independent of its shape. Without it a sparse sheet that
	// declares one cell in each of a million rows at column XFD would cost
	// maxRows*maxColumns of padding.
	maxCells = 8 << 20 // 8388608

	// maxSharedStrings and maxCellXfs bound the two lookup tables that cell
	// records index into. Both are read fully into memory before any sheet
	// is parsed.
	maxSharedStrings = 4 << 20 // 4194304
	maxCellXfs       = 1 << 20 // 1048576

	// maxNumFmts bounds the custom number-format table in xl/styles.xml.
	maxNumFmts = 65536
)
