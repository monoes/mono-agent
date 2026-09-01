package control

// RemoveDuplicatesNodeSchema documents the config keys
// RemoveDuplicatesNode.Execute reads out of its map[string]interface{}
// config — see SetNodeSchema's doc comment for why this is a companion
// struct rather than the runtime config, and internal/tools/schemagen for
// the tag grammar.
//
// Two drift findings vs. the hand-written schemas/core.remove_duplicates.json
// this replaces:
//
//   - The hand-written schema marked "field" as required. Execute treats it
//     as optional: an empty "field" falls back to deduplicating by the
//     entire JSON-serialized item, which is a valid, intentional mode, not
//     an error. Marked optional here.
//   - "keep" ("first" default, or "last") is a real config key Execute
//     reads and validates, with no field in the hand-written schema at all.
type RemoveDuplicatesNodeSchema struct {
	Field string `json:"field" schema:"label=Deduplicate By Field,type=text,placeholder=id,help=Items with the same value in this field are deduplicated. Leave empty to deduplicate by the entire item."`

	Keep string `json:"keep" schema:"label=Keep,type=select,options=first|last,default=first,help=Which occurrence to keep when duplicates are found."`
}
