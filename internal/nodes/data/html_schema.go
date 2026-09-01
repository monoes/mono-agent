package data

// HTMLNodeSchema documents the config keys HTMLNode.Execute reads out of its
// map[string]interface{} config — see internal/nodes/control.SetNodeSchema's
// doc comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// This matches the hand-written schemas/data.html.json field-for-field —
// see internal/nodes/data/schema_keys_test.go's
// TestHTMLNodeUsesCodeOperationNames for the regression this schema already
// covers (operation names extract/extract_all/text/generate).
type HTMLNodeSchema struct {
	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=extract|extract_all|text|generate,default=extract"`

	Field string `json:"field" schema:"label=HTML Field,type=text,required,placeholder=html_content,depends_on_key=operation,depends_on_values=extract|extract_all|text"`

	Selector string `json:"selector" schema:"label=CSS Selector,type=text,placeholder=div.article p,depends_on_key=operation,depends_on_values=extract|extract_all"`

	Attribute string `json:"attribute" schema:"label=Attribute (optional),type=text,placeholder=href,help=Extract this HTML attribute instead of text content.,depends_on_key=operation,depends_on_values=extract|extract_all"`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,help=Defaults to the HTML field， overwriting it."`

	Template string `json:"template" schema:"label=HTML Template,type=textarea,rows=5,placeholder=<div>{{.title}}</div>,depends_on_key=operation,depends_on_values=generate"`
}
