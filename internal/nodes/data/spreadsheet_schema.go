package data

// SpreadsheetNodeSchema documents the config keys SpreadsheetNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// "has_header" has no entry in the hand-written schemas/data.spreadsheet.json
// this replaces — Execute has always read it (defaulting to true) to decide
// whether the first row is a header on read, and whether to write one on
// write, but the hand-written schema never exposed a way to turn it off.
//
// Execute also accepts a "sheet" config alias for "sheet_name" (checked only
// when "sheet_name" is empty) — that's an undocumented alias for the same
// field, not a distinct config value, so it isn't given its own schema
// entry.
type SpreadsheetNodeSchema struct {
	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=read_csv|write_csv|read_xlsx|write_xlsx,default=read_csv"`

	FilePath string `json:"file_path" schema:"label=File Path,type=text,required,placeholder=/tmp/data.csv"`

	SheetName string `json:"sheet_name" schema:"label=Sheet Name,type=text,default=Sheet1,depends_on_key=operation,depends_on_values=read_xlsx|write_xlsx"`

	HasHeader bool `json:"has_header" schema:"label=Has Header Row,type=boolean,default=true,help=On read， treat the first row as a header. On write， emit a header row."`
}
