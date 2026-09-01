package control

// WaitNodeSchema documents the config keys WaitNode.Execute reads out of its
// map[string]interface{} config — see SetNodeSchema's doc comment for why
// this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// Matches the hand-written schemas/core.wait.json this replaces
// field-for-field; no drift found.
type WaitNodeSchema struct {
	Duration float64 `json:"duration" schema:"label=Wait Duration (seconds),type=number,required,default=5,min=1,max=3600"`
}
