package documentsnodes

// RenderNodeSchema documents the config keys RenderNode.Execute reads out
// of its map[string]interface{} config.
type RenderNodeSchema struct {
	DocType       string `json:"doc_type" schema:"label=Document Type,type=select,required,options=cv|cover_letter|tender_proposal,help=Which document to generate."`
	Data          string `json:"data" schema:"label=Data,type=code,language=json,required,help=A JSON object matching the chosen document type's fields."`
	ApplicationID string `json:"application_id" schema:"label=Application ID,type=text,help=Optional job/tender application to link this document to."`
	ProfileID     string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns the generated document."`
}
