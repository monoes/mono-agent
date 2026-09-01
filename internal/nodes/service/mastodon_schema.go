package service

// MastodonNodeSchema documents the config keys MastodonNode.Execute reads
// out of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// credential_platform: mastodon
type MastodonNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Mastodon Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=publish_status|get_timeline|get_account|favourite|boost,default=publish_status"`

	Text string `json:"text" schema:"label=Status Text,type=textarea,rows=4,depends_on_key=operation,depends_on_values=publish_status"`

	Visibility string `json:"visibility" schema:"label=Visibility,type=select,options=public|unlisted|private|direct,depends_on_key=operation,depends_on_values=publish_status"`

	SpoilerText string `json:"spoiler_text" schema:"label=Content Warning,type=text,depends_on_key=operation,depends_on_values=publish_status"`

	MediaIDs []string `json:"media_ids" schema:"label=Media IDs,type=array,item_type=text,help=Media attachment IDs to attach to the status.,depends_on_key=operation,depends_on_values=publish_status"`

	Limit float64 `json:"limit" schema:"label=Limit,type=number,default=20,depends_on_key=operation,depends_on_values=get_timeline"`

	StatusID string `json:"status_id" schema:"label=Status ID,type=text,help=Target status for favourite or boost.,depends_on_key=operation,depends_on_values=favourite|boost"`

	Instance string `json:"instance" schema:"label=Instance URL,type=text,default=https://mastodon.social,help=Mastodon instance to talk to."`

	AccessToken string `json:"access_token" schema:"label=Access Token,type=text,help=OAuth token from Mastodon settings. Resolved from the saved connection when omitted."`
}
