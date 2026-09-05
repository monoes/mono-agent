package matchingnodes

// EvaluateNodeSchema documents the config keys EvaluateNode.Execute reads
// out of its map[string]interface{} config.
type EvaluateNodeSchema struct {
	ApplicationID string `json:"application_id" schema:"label=Application ID,type=text,required,help=The job application to score."`
	Runtime       string `json:"runtime" schema:"label=Runtime,type=text,default=claude,help=Local agent runtime to use."`
	ProfileID     string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns this application."`
}
