package control

// StopErrorNodeSchema documents the config keys StopErrorNode.Execute reads
// out of its map[string]interface{} config — see SetNodeSchema's doc
// comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// Matches the hand-written schemas/core.stop_error.json this replaces
// field-for-field; no drift found.
type StopErrorNodeSchema struct {
	Message string `json:"message" schema:"label=Error Message,type=text,required,placeholder=Workflow stopped: condition not met"`
}
