package control

// FilterNodeSchema documents the config keys FilterNode.Execute reads out of
// its map[string]interface{} config — see SetNodeSchema's doc comment for
// why this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// "mode" has no entry in the hand-written schemas/core.filter.json this
// replaces — FilterNode.Execute has supported it since it was written, but
// the schema never exposed a way to set it from the UI. Generating from the
// struct (rather than hand-copying the old JSON) surfaces that gap.
type FilterNodeSchema struct {
	Condition string `json:"condition" schema:"label=Keep items where,type=text,required,placeholder={{ $json.age > 18 }},help=Items where this evaluates to true are passed through."`

	Mode string `json:"mode" schema:"label=Mode,type=select,options=keep|remove,default=keep,help=\"keep\" passes through items where the condition is true (default)， \"remove\" passes through items where it is false."`
}
