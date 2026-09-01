package peoplenodes

// SyncOutlookMessageNodeSchema documents the config keys
// SyncOutlookMessageNode.Execute reads out of its map[string]interface{}
// config — see internal/nodes/control.SetNodeSchema's doc comment for why
// this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type SyncOutlookMessageNodeSchema struct {
	Source string `json:"source" schema:"label=Source Label,type=text,default=outlook,help=Stored as the message's source， e.g. outlook， gmail"`

	Direction string `json:"direction" schema:"label=Direction,type=select,options=inbound|outbound,default=inbound,help=inbound: sync each message's sender as the person (use with an inbox read). outbound: sync each recipient as the person instead (use with a sentitems/sent-mail read)."`

	ProfileID string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns the synced people/messages. Defaults to 'default' — set this explicitly if this workflow belongs to a different profile (find the ID via 'monoagentcli profile list')."`
}
