package service

// DevToNodeSchema documents the config keys DevToNode.Execute reads out of
// its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. It is never constructed or
// used at runtime; this struct exists solely to generate
// internal/workflow/schemas/service.devto.json.
//
// credential_platform: devto
type DevToNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Dev.to Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=publish_article|list_articles|get_article|list_comments|create_comment,default=publish_article"`

	Title string `json:"title" schema:"label=Title,type=text,depends_on_key=operation,depends_on_values=publish_article"`

	BodyMarkdown string `json:"body_markdown" schema:"label=Body (Markdown),type=textarea,rows=8,help=Article body for publish_article， or comment text for create_comment.,depends_on_key=operation,depends_on_values=publish_article|create_comment"`

	Tags string `json:"tags" schema:"label=Tags,type=text,placeholder=e.g. go， webdev， tutorial,help=Comma-separated tags for the article.,depends_on_key=operation,depends_on_values=publish_article"`

	Series string `json:"series" schema:"label=Series,type=text,depends_on_key=operation,depends_on_values=publish_article"`

	Published bool `json:"published" schema:"label=Published,type=boolean,default=true,help=false saves the article as a draft.,depends_on_key=operation,depends_on_values=publish_article"`

	CanonicalURL string `json:"canonical_url" schema:"label=Canonical URL,type=text,depends_on_key=operation,depends_on_values=publish_article"`

	Page float64 `json:"page" schema:"label=Page,type=number,default=1,depends_on_key=operation,depends_on_values=list_articles"`

	PerPage float64 `json:"per_page" schema:"label=Per Page,type=number,default=30,depends_on_key=operation,depends_on_values=list_articles"`

	ArticleID string `json:"article_id" schema:"label=Article ID,type=text,help=Numeric Dev.to article id.,depends_on_key=operation,depends_on_values=get_article|list_comments|create_comment"`
}
