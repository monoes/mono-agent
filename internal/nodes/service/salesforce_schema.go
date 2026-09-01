package service

// SalesforceNodeSchema documents the config keys SalesforceNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// Auth (instance_url, access_token) is resolved by the credential layer
// from the saved connection and is not exposed as a schema field, matching
// the hand-written schema this replaces.
//
// credential_platform: salesforce
type SalesforceNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Salesforce Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=query|get_record|create_record|update_record|delete_record|describe_object,default=query"`

	ObjectType string `json:"object_type" schema:"label=Object Type,type=text,placeholder=Contact,depends_on_key=operation,depends_on_values=get_record|create_record|update_record|delete_record|describe_object"`

	RecordID string `json:"record_id" schema:"label=Record ID,type=text,depends_on_key=operation,depends_on_values=get_record|update_record|delete_record"`

	SOQL string `json:"soql" schema:"label=SOQL Query,type=code,depends_on_key=operation,depends_on_values=query"`

	Fields string `json:"fields" schema:"label=Fields (JSON),type=textarea,rows=5,depends_on_key=operation,depends_on_values=create_record|update_record"`
}
