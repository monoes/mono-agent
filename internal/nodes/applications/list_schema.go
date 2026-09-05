// internal/nodes/applications/list_schema.go
package applicationsnodes

// ListNodeSchema documents the config keys ListNode.Execute reads out of
// its map[string]interface{} config.
type ListNodeSchema struct {
	Kind      string `json:"kind" schema:"label=Kind Filter,type=select,options=|job|tender,help=Leave blank to include both kinds."`
	Status    string `json:"status" schema:"label=Status Filter,type=select,options=|pending|applied|rejected|cancelled,help=Leave blank to include all statuses."`
	Tag       string `json:"tag" schema:"label=Tag Filter,type=text,help=Leave blank to include all tags."`
	ProfileID string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile's applications to list."`
}
