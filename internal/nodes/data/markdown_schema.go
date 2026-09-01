package data

// MarkdownNodeSchema documents the config keys MarkdownNode.Execute reads
// out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// This matches the hand-written schemas/data.markdown.json field-for-field.
type MarkdownNodeSchema struct {
	Field string `json:"field" schema:"label=Input Field (Markdown),type=text,required,placeholder=content,help=Converts Markdown to HTML."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,help=Defaults to the input field， overwriting it."`

	Unsafe bool `json:"unsafe" schema:"label=Allow Raw HTML,type=boolean,default=false"`
}
