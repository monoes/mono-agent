// internal/nodes/apply/prepare_schema.go
package applynodes

// PrepareNodeSchema documents the config keys PrepareNode.Execute reads
// out of its map[string]interface{} config.
type PrepareNodeSchema struct {
	ApplicationID   string `json:"application_id" schema:"label=Application ID,type=text,required,help=The job application to prepare documents for."`
	CVData          string `json:"cv_data" schema:"label=CV Data,type=code,language=json,required,help=A JSON object matching documents.CVData's fields."`
	CoverLetterData string `json:"cover_letter_data" schema:"label=Cover Letter Data,type=code,language=json,required,help=A JSON object matching documents.CoverLetterData's fields."`
	ProfileID       string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns this application."`
}
