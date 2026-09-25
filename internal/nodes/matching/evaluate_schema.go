package matchingnodes

// EvaluateNodeSchema documents the config keys EvaluateNode.Execute reads
// out of its map[string]interface{} config.
type EvaluateNodeSchema struct {
	ApplicationID string `json:"application_id" schema:"label=Application ID,type=text,required,help=The job application to score."`
	Runtime       string `json:"runtime" schema:"label=Runtime,type=text,default=claude,help=Local agent runtime to use (e.g. claude)， or jev / jev:<model> to score with TypeSafe Jev: one request answers the gates and rubric dimensions and the verdict is computed with the same weights and bands. Needs the vault secret typesafe or TYPESAFE_API_KEY (no fallback)， sends the job posting and profile excerpts to TypeSafe."`
	ProfileID     string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns this application."`
}
