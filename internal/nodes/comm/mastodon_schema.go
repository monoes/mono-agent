package comm

// MastodonNodeSchema documents the config keys MastodonNode.Execute reads
// out of its map[string]interface{} config — see BlueskyNodeSchema's doc
// comment for why "instance_url" and "access_token" are not fields here
// (they arrive via credential_id resolution in
// internal/workflow/execution.go), and internal/tools/schemagen for the tag
// grammar.
//
// credential_platform: mastodon
type MastodonNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Mastodon Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=create_status|get_status_metrics,default=create_status"`

	Text string `json:"text" schema:"label=Status Text,type=textarea,rows=4,depends_on_key=operation,depends_on_values=create_status"`

	Visibility string `json:"visibility" schema:"label=Visibility,type=select,options=public|unlisted|private|direct,default=public,depends_on_key=operation,depends_on_values=create_status"`

	InReplyToID string `json:"in_reply_to_id" schema:"label=In Reply To (status ID),type=text,depends_on_key=operation,depends_on_values=create_status"`

	StatusID string `json:"status_id" schema:"label=Status ID,type=text,depends_on_key=operation,depends_on_values=get_status_metrics"`
}
