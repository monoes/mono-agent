package service

// JiraNodeSchema documents the config keys JiraNode.Execute reads out of its
// map[string]interface{} config — see internal/nodes/control/set_schema.go's
// doc comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// Auth fields (domain/base_url, email, api_token, access_token) are resolved
// by the credential layer from the saved connection and are not exposed as
// schema fields here — matching the hand-written schema this replaces,
// which never listed them either.
//
// "project_key" was a resource_picker (resource: {type: "projects"}) in the
// hand-written schemas/service.jira.json this replaces. schemagen's
// schema:"..." tag grammar has no way to populate NodeSchemaField.Resource
// (see internal/tools/schemagen's package doc comment — "resource" is not a
// recognized tag key), so it degrades to a plain text field here. This is a
// schemagen limitation, not specific to this node.
//
// credential_platform: jira
type JiraNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Jira Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=list_issues|create_issue|update_issue|get_issue|add_comment|list_projects,default=list_issues"`

	ProjectKey string `json:"project_key" schema:"label=Project,type=text,depends_on_key=operation,depends_on_values=list_issues|create_issue"`

	JQL string `json:"jql" schema:"label=JQL Query,type=code,help=Custom JQL; overrides project/status.,depends_on_key=operation,depends_on_values=list_issues"`

	Status string `json:"status" schema:"label=Status,type=text,depends_on_key=operation,depends_on_values=list_issues"`

	IssueKey string `json:"issue_key" schema:"label=Issue Key,type=text,placeholder=PROJ-123,depends_on_key=operation,depends_on_values=update_issue|get_issue|add_comment"`

	Summary string `json:"summary" schema:"label=Summary,type=text,depends_on_key=operation,depends_on_values=create_issue|update_issue"`

	Description string `json:"description" schema:"label=Description,type=textarea,depends_on_key=operation,depends_on_values=create_issue|update_issue"`

	IssueType string `json:"issue_type" schema:"label=Issue Type,type=text,default=Task,depends_on_key=operation,depends_on_values=create_issue"`

	Comment string `json:"comment" schema:"label=Comment,type=textarea,depends_on_key=operation,depends_on_values=add_comment"`
}
