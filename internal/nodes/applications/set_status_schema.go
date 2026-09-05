// internal/nodes/applications/set_status_schema.go
package applicationsnodes

// SetStatusNodeSchema documents the config keys SetStatusNode.Execute
// reads out of its map[string]interface{} config.
type SetStatusNodeSchema struct {
	ID        string `json:"id" schema:"label=Application ID,type=text,required,help=The application to transition."`
	Status    string `json:"status" schema:"label=New Status,type=select,required,options=rejected|cancelled,help=Must be a valid transition from the application's current status. \"applied\" is not offered here — use the application send CLI command， which requires an explicit human action."`
	Actor     string `json:"actor" schema:"label=Actor,type=select,default=system,options=user|system,help=Who performed this transition."`
	Note      string `json:"note" schema:"label=Note,type=textarea,help=Optional note recorded in the transition ledger."`
	ProfileID string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns this application."`
}
