package data

// CompressionNodeSchema documents the config keys CompressionNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// "filename" has no entry in the hand-written schemas/data.compression.json
// this replaces — Execute reads it (defaulting to "data") to name the entry
// written inside the zip archive for operation=zip. The hand-written schema
// never exposed a way to set it.
type CompressionNodeSchema struct {
	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=zip|unzip|gzip|gunzip,default=zip"`

	InputField string `json:"input_field" schema:"label=Input Field,type=text,required,placeholder=file_data,help=Item field containing the base64-encoded input data."`

	OutputField string `json:"output_field" schema:"label=Output Field Name,type=text,default=compressed"`

	Filename string `json:"filename" schema:"label=Archive Entry Filename,type=text,default=data,help=Name given to the entry inside the zip archive (operation=zip only).,depends_on_key=operation,depends_on_values=zip"`
}
