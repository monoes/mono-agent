package service

// HubSpotNodeSchema documents the config keys HubSpotNode.Execute reads out
// of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. It is never constructed or
// used at runtime; this struct exists solely to generate
// internal/workflow/schemas/service.hubspot.json.
//
// credential_platform: hubspot
type HubSpotNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=HubSpot Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=list_contacts|get_contact|create_contact|update_contact|list_deals|create_deal|update_deal|list_companies|create_company|update_company,default=list_contacts"`

	ObjectID string `json:"object_id" schema:"label=Object ID,type=text,depends_on_key=operation,depends_on_values=get_contact|update_contact|update_deal|update_company"`

	Properties string `json:"properties" schema:"label=Properties (JSON),type=textarea,rows=5,depends_on_key=operation,depends_on_values=create_contact|update_contact|create_deal|update_deal|create_company|update_company"`

	Limit float64 `json:"limit" schema:"label=Limit,type=number,default=10,depends_on_key=operation,depends_on_values=list_contacts|list_deals|list_companies"`

	After string `json:"after" schema:"label=Pagination Cursor,type=text,depends_on_key=operation,depends_on_values=list_contacts|list_deals|list_companies"`
}
