package peoplenodes

// PeopleSaveNodeSchema documents the config keys PeopleSaveNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// "profile_id" has no entry in the hand-written schemas/people.save.json
// this replaces — Execute has read it (defaulting to "default") since it
// was written, but the schema only ever exposed "platform". Generating from
// the struct surfaces that gap.
type PeopleSaveNodeSchema struct {
	Platform string `json:"platform" schema:"label=Platform Override,type=select,options=|linkedin|instagram|x|tiktok,default=,help=Force a specific platform. Leave blank to use the platform field from each input item."`

	ProfileID string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns the saved people. Defaults to 'default' — set this explicitly if this workflow belongs to a different profile (find the ID via 'monoagentcli profile list')."`
}
