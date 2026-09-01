package control

// IfNodeSchema documents the config keys IfNode.Execute reads out of its
// map[string]interface{} config — see SetNodeSchema's doc comment for why
// this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// "mode" has no entry in the hand-written schemas/core.if.json this
// replaces, for the same reason noted on FilterNodeSchema: the field has
// always been read by Execute, but the schema never exposed it.
type IfNodeSchema struct {
	Condition string `json:"condition" schema:"label=Condition Expression,type=text,required,placeholder=e.g. {{ $json.status == 'active' }},help=Expression evaluated against the current item. Routes to 'true' or 'false' output."`

	Mode string `json:"mode" schema:"label=Mode,type=select,options=all|per_item,default=all,help=\"all\" evaluates the condition once against the first item and routes the whole batch (default)， \"per_item\" evaluates it independently for every item."`
}
