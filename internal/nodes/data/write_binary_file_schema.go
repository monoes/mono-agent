package data

// WriteBinaryFileNodeSchema documents the config keys
// WriteBinaryFileNode.Execute reads out of its map[string]interface{}
// config — see internal/nodes/control.SetNodeSchema's doc comment for why
// this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// This matches the hand-written schemas/data.write_binary_file.json
// field-for-field — see
// internal/nodes/data/schema_keys_test.go's
// TestWriteBinaryFileUsesSchemaKeys for the regression this schema already
// covers (keys "file_path"/"field", not "path"/"data_field").
type WriteBinaryFileNodeSchema struct {
	FilePath string `json:"file_path" schema:"label=File Path,type=text,required,placeholder=/tmp/output.pdf"`

	Field string `json:"field" schema:"label=Binary Data Field,type=text,required,placeholder=pdf_bytes"`

	Encoding string `json:"encoding" schema:"label=Field Encoding,type=select,options=base64|utf8,default=base64"`

	CreateDirs bool `json:"create_dirs" schema:"label=Create Parent Directories,type=boolean,default=true"`
}
