package discoverynodes

// SearchJobsNodeSchema documents the config keys SearchJobsNode.Execute
// reads out of its map[string]interface{} config.
type SearchJobsNodeSchema struct {
	Keywords  string `json:"keywords" schema:"label=Keywords,type=text,required,help=Job search keywords， e.g. 'backend engineer'."`
	Location  string `json:"location" schema:"label=Location,type=text,help=Job location filter."`
	Source    string `json:"source" schema:"label=Source,type=select,default=linkedin,options=linkedin|arbeitnow|jobicy,help=Which job board to search."`
	Limit     int    `json:"limit" schema:"label=Limit,type=number,default=25,help=Maximum results to import (capped at 100)."`
	ProfileID string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns the imported applications."`
}
