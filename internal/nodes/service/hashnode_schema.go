package service

// HashnodeNodeSchema documents the config keys HashnodeNode.Execute reads
// out of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. It is never constructed or
// used at runtime; this struct exists solely to generate
// internal/workflow/schemas/service.hashnode.json.
//
// credential_platform: hashnode
type HashnodeNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Hashnode Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=publish_post|list_posts|get_post,default=publish_post"`

	PublicationID string `json:"publication_id" schema:"label=Publication ID,type=text,depends_on_key=operation,depends_on_values=publish_post"`

	Title string `json:"title" schema:"label=Title,type=text,depends_on_key=operation,depends_on_values=publish_post"`

	ContentMarkdown string `json:"content_markdown" schema:"label=Content (Markdown),type=textarea,rows=8,depends_on_key=operation,depends_on_values=publish_post"`

	Tags string `json:"tags" schema:"label=Tags,type=array,item_type=text,help=Tag slugs (e.g. go， webdev).,depends_on_key=operation,depends_on_values=publish_post"`

	Subtitle string `json:"subtitle" schema:"label=Subtitle,type=text,depends_on_key=operation,depends_on_values=publish_post"`

	Slug string `json:"slug" schema:"label=Slug,type=text,help=Custom slug for publish_post， or the post slug to fetch for get_post.,depends_on_key=operation,depends_on_values=publish_post|get_post"`

	PublicationHost string `json:"publication_host" schema:"label=Publication Host,type=text,placeholder=e.g. myblog.hashnode.dev,depends_on_key=operation,depends_on_values=list_posts|get_post"`

	First float64 `json:"first" schema:"label=First,type=number,default=10,depends_on_key=operation,depends_on_values=list_posts"`

	Token string `json:"token" schema:"label=Personal Access Token,type=text,help=Hashnode token. Resolved from the saved connection when omitted."`
}
