package data

// DateTimeNodeSchema documents the config keys DateTimeNode.Execute reads
// out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// "output_field" has no entry in the hand-written schemas/data.datetime.json
// this replaces — Execute has always read it (defaulting to the same field,
// overwriting it) but the hand-written schema never exposed it, unlike every
// other node in this package that supports the same default-to-input-field
// convention (data.html, data.markdown, data.xml).
type DateTimeNodeSchema struct {
	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=format|parse|add|subtract|diff|now,default=format"`

	Field string `json:"field" schema:"label=Date Field,type=text,placeholder=created_at,depends_on_key=operation,depends_on_values=format|parse|add|subtract|diff"`

	Field2 string `json:"field2" schema:"label=Second Date Field,type=text,placeholder=updated_at,depends_on_key=operation,depends_on_values=diff"`

	InputFormat string `json:"input_format" schema:"label=Input Format,type=text,placeholder=2006-01-02T15:04:05Z07:00,default=2006-01-02T15:04:05Z07:00,help=Go time layout string used to parse the input date field(s)."`

	OutputFormat string `json:"output_format" schema:"label=Output Format,type=text,placeholder=2006-01-02T15:04:05Z07:00,default=2006-01-02T15:04:05Z07:00,help=Go time layout string."`

	Duration string `json:"duration" schema:"label=Duration,type=text,placeholder=24h,depends_on_key=operation,depends_on_values=add|subtract"`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,help=Defaults to the date field， overwriting it."`
}
