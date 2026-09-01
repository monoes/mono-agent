package control

// SortNodeSchema documents the config keys SortNode.Execute reads out of its
// map[string]interface{} config — see SetNodeSchema's doc comment for why
// this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// "type" (sort comparison mode: "string" default, "number", or "date") has
// no entry in the hand-written schemas/core.sort.json this replaces —
// SortNode.Execute has read and switched on it since it was written, but
// the schema never exposed a way to set it from the UI, so every sort ran
// as a string compare regardless of the underlying data type. Surfaced here.
//
// Execute also accepts "order" as a fallback for "direction" (checking
// "direction" first) — the schema exposes only "direction", matching what
// the hand-written schema already used and what Execute prefers.
type SortNodeSchema struct {
	Field string `json:"field" schema:"label=Sort By Field,type=text,required,placeholder=created_at"`

	Direction string `json:"direction" schema:"label=Direction,type=select,required,options=asc|desc,default=asc"`

	Type string `json:"type" schema:"label=Value Type,type=select,options=string|number|date,default=string,help=How field values are compared. Falls back to string comparison if a value can't be parsed as the selected type."`
}
