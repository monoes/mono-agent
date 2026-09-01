package control

// SwitchNodeSchema documents the config keys SwitchNode.Execute reads out of
// its map[string]interface{} config — see SetNodeSchema's doc comment for
// why this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// Two drift findings vs. the hand-written schemas/core.switch.json this
// replaces:
//
//   - The hand-written schema declared "fallthrough" with default=true.
//     Execute reads it as `config["fallthrough"].(bool)` with no presence
//     check, so an absent key resolves to Go's zero value, false — i.e. the
//     node's real default behavior is "first match wins", the opposite of
//     what the schema claimed. The default is corrected to false here.
//   - "default_handle" (handle name for unmatched items, default "default")
//     is a real config key Execute reads, with no field in the hand-written
//     schema at all. Added here.
//
// Execute also accepts "expression" as a fallback for "field" (checking
// "field" first) — the schema exposes only "field", matching what the
// hand-written schema already used and what Execute prefers.
type SwitchNodeSchema struct {
	Field string `json:"field" schema:"label=Field to Switch On,type=text,required,placeholder={{ $json.status }},help=Value expression to match against cases."`

	Cases string `json:"cases" schema:"label=Cases,type=array,item_type=text,required,help=Each value becomes an output handle. Type a value and press Enter."`

	DefaultHandle string `json:"default_handle" schema:"label=Default Handle,type=text,default=default,help=Output handle name for items that don't match any case."`

	Fallthrough bool `json:"fallthrough" schema:"label=Fallthrough to 'default',type=boolean,default=false,help=If enabled， an item can match more than one case instead of stopping at the first match."`
}
