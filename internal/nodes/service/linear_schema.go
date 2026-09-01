package service

// LinearNodeSchema documents the config keys LinearNode.Execute reads out of
// its map[string]interface{} config — see internal/nodes/control/set_schema.go's
// doc comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// Auth (api_key / access_token) is resolved by the credential layer from the
// saved connection and is not exposed as a schema field, matching the
// hand-written schema this replaces.
//
// "team_id" was a resource_picker (resource: {type: "teams"}) in the
// hand-written schemas/service.linear.json this replaces; schemagen's
// schema:"..." tag grammar has no way to populate NodeSchemaField.Resource
// (see internal/tools/schemagen's package doc comment), so it degrades to a
// plain text field here — a schemagen limitation, not specific to this
// node.
//
// "state_id" has no entry in the hand-written schemas/service.linear.json
// this replaces — createIssue and updateIssue both read it
// (`config["state_id"]`) to set the issue's workflow state, but the schema
// never exposed a way to set it from the UI. Generating from the struct
// surfaces that gap; it is added below.
//
// credential_platform: linear
type LinearNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Linear Connection,type=credential_picker,required"`

	TeamID string `json:"team_id" schema:"label=Team,type=text"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=list_issues|get_issue|create_issue|update_issue|list_teams|list_projects,default=list_issues"`

	IssueID string `json:"issue_id" schema:"label=Issue ID,type=text,depends_on_key=operation,depends_on_values=get_issue|update_issue"`

	Title string `json:"title" schema:"label=Issue Title,type=text,depends_on_key=operation,depends_on_values=create_issue|update_issue"`

	Description string `json:"description" schema:"label=Description,type=textarea,rows=4,depends_on_key=operation,depends_on_values=create_issue|update_issue"`

	Priority string `json:"priority" schema:"label=Priority,type=select,options=0|1|2|3|4,default=0,depends_on_key=operation,depends_on_values=create_issue|update_issue"`

	StateID string `json:"state_id" schema:"label=State,type=text,depends_on_key=operation,depends_on_values=create_issue|update_issue"`

	ProjectID string `json:"project_id" schema:"label=Project,type=text,depends_on_key=operation,depends_on_values=list_projects|create_issue"`
}
