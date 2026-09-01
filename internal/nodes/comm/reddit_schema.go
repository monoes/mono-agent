package comm

// RedditNodeSchema documents the config keys RedditNode.Execute reads out of
// its map[string]interface{} config — see BlueskyNodeSchema's doc comment
// for why "access_token" is not a field here (it arrives via credential_id
// resolution in internal/workflow/execution.go), and
// internal/tools/schemagen for the tag grammar.
//
// credential_platform: reddit
type RedditNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Reddit Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=submit_post|reply_to_comment|list_comments|get_post_metrics,default=submit_post"`

	Subreddit string `json:"subreddit" schema:"label=Subreddit,type=text,depends_on_key=operation,depends_on_values=submit_post"`

	Title string `json:"title" schema:"label=Title,type=text,depends_on_key=operation,depends_on_values=submit_post"`

	Text string `json:"text" schema:"label=Body / Comment Text,type=textarea,rows=4,depends_on_key=operation,depends_on_values=submit_post|reply_to_comment"`

	URL string `json:"url" schema:"label=Link URL (link post instead of self post),type=text,depends_on_key=operation,depends_on_values=submit_post"`

	ThingID string `json:"thing_id" schema:"label=Post/Comment ID (e.g. t3_abc123 for a post，t1_xyz for a comment),type=text,depends_on_key=operation,depends_on_values=reply_to_comment|list_comments|get_post_metrics"`
}
