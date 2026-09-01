package control

// HumanInLoopNodeSchema documents the config keys HumanInLoopNode.Execute
// reads out of its map[string]interface{} config — see SetNodeSchema's doc
// comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// Matches the hand-written schemas/core.human_in_loop.json this replaces
// field-for-field; no drift found.
type HumanInLoopNodeSchema struct {
	ReadonlyFields string `json:"readonly_fields" schema:"label=Read-Only Fields,type=array,item_type=text,help=Item keys shown to the reviewer as read-only context."`

	EditableFields string `json:"editable_fields" schema:"label=Editable Fields,type=array,item_type=text,help=Item keys the reviewer can edit before approving. If left empty， the whole item is editable."`

	TimeoutMinutes float64 `json:"timeout_minutes" schema:"label=Timeout (minutes),type=number,default=0,help=Max time to wait for a human decision. 0 = unlimited."`
}
