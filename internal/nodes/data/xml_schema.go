package data

// XMLNodeSchema documents the config keys XMLNode.Execute reads out of its
// map[string]interface{} config — see internal/nodes/control.SetNodeSchema's
// doc comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// This matches the hand-written schemas/data.xml.json field-for-field.
type XMLNodeSchema struct {
	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=parse|generate,default=parse"`

	Field string `json:"field" schema:"label=XML Field,type=text,required,placeholder=xml_content"`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,help=Defaults to the XML field， overwriting it."`

	RootElement string `json:"root_element" schema:"label=Root Element Name,type=text,default=root,depends_on_key=operation,depends_on_values=generate"`
}
