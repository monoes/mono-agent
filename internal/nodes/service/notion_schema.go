package service

// NotionNodeSchema documents the config keys NotionNode.Execute reads out of
// its map[string]interface{} config — see internal/nodes/control/set_schema.go's
// doc comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// Auth (token / access_token) is resolved by the credential layer from the
// saved connection and is not exposed as a schema field, matching the
// hand-written schema this replaces.
//
// "database_id" was a resource_picker (resource: {type: "databases"}) in
// the hand-written schemas/service.notion.json this replaces; schemagen's
// schema:"..." tag grammar has no way to populate NodeSchemaField.Resource
// (see internal/tools/schemagen's package doc comment), so it degrades to a
// plain text field here — a schemagen limitation, not specific to this
// node.
//
// "sorts" has no entry in the hand-written schemas/service.notion.json this
// replaces — queryDatabase reads it (`config["sorts"]`) to sort query
// results, but the schema never exposed a way to set it from the UI.
// Generating from the struct surfaces that gap; it is added below.
//
// credential_platform: notion
type NotionNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Notion Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=query_database|get_database|create_database|create_page|update_page|get_page|append_blocks,default=query_database"`

	DatabaseID string `json:"database_id" schema:"label=Database,type=text,depends_on_key=operation,depends_on_values=query_database|get_database|create_page"`

	PageID string `json:"page_id" schema:"label=Page ID,type=text,depends_on_key=operation,depends_on_values=update_page|get_page|create_database|append_blocks"`

	ParentID string `json:"parent_id" schema:"label=Parent ID,type=text,help=Parent page for create_database， or parent database for create_page.,depends_on_key=operation,depends_on_values=create_page|create_database"`

	Properties string `json:"properties" schema:"label=Page Properties (JSON),type=textarea,rows=5,depends_on_key=operation,depends_on_values=create_page|update_page|create_database"`

	Children string `json:"children" schema:"label=Blocks (JSON),type=textarea,rows=5,depends_on_key=operation,depends_on_values=create_page|append_blocks"`

	Filter string `json:"filter" schema:"label=Filter (JSON),type=textarea,rows=4,depends_on_key=operation,depends_on_values=query_database"`

	Sorts string `json:"sorts" schema:"label=Sorts (JSON),type=textarea,rows=3,help=JSON array of Notion sort objects， e.g. [{\"property\": \"Name\"， \"direction\": \"ascending\"}].,depends_on_key=operation,depends_on_values=query_database"`

	PageSize float64 `json:"page_size" schema:"label=Page Size,type=number,depends_on_key=operation,depends_on_values=query_database"`
}
