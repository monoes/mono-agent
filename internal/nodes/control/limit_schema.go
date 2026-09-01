package control

// LimitNodeSchema documents the config keys LimitNode.Execute reads out of
// its map[string]interface{} config — see SetNodeSchema's doc comment for
// why this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// The hand-written schemas/core.limit.json this replaces had no "max"
// bound; LimitNode.Execute actually enforces max_items in [1, 10000]. That
// upper bound is added here.
type LimitNodeSchema struct {
	MaxItems float64 `json:"max_items" schema:"label=Max Items,type=number,required,default=10,min=1,max=10000,help=Only the first N items pass through."`
}
