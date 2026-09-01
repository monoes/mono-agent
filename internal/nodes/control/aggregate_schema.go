package control

// AggregateNodeSchema documents the config keys AggregateNode.Execute reads
// out of its map[string]interface{} config — see SetNodeSchema's doc
// comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// AggregateNode.Execute (via parseAggregateOps) actually supports two config
// shapes: this flat single-operation form ("operation"/"field"/"output_field"
// as top-level keys, matching the hand-written schemas/core.aggregate.json
// this replaces), or a multi-operation "operations" array. The schema only
// exposes the flat form, matching the existing hand-written schema's UI
// surface; the array form remains available to callers that build config
// programmatically.
type AggregateNodeSchema struct {
	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=count|sum|avg|min|max|collect,default=count"`

	Field string `json:"field" schema:"label=Field Name,type=text,placeholder=amount,help=Field to aggregate. Not needed for 'count'.,depends_on_key=operation,depends_on_values=sum|avg|min|max|collect"`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,help=Defaults to the operation name (e.g. 'sum')."`

	GroupBy string `json:"group_by" schema:"label=Group By Field,type=text,placeholder=category,help=Optional: group results by this field."`
}
