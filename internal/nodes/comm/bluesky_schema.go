package comm

// BlueskyNodeSchema documents the config keys BlueskyNode.Execute reads out
// of its map[string]interface{} config — see control.SetNodeSchema's doc
// comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// "identifier" and "app_password" are intentionally NOT fields here even
// though Execute reads config["identifier"] / config["app_password"]
// directly: for a credential_platform node, internal/workflow/execution.go
// resolves credential_id into the connection's stored Data map and merges
// those keys into config before Execute runs, so the UI only ever needs to
// expose the credential_id picker — matching the hand-written schema this
// replaces.
//
// credential_platform: bluesky
type BlueskyNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Bluesky Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=create_post|get_post_metrics,default=create_post"`

	Text string `json:"text" schema:"label=Post Text,type=textarea,rows=4,depends_on_key=operation,depends_on_values=create_post"`

	PostURI string `json:"post_uri" schema:"label=Post URI (at://...),type=text,depends_on_key=operation,depends_on_values=get_post_metrics"`
}
