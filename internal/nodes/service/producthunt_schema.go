package service

// ProductHuntNodeSchema documents the config keys ProductHuntNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// credential_platform: producthunt
type ProductHuntNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Product Hunt Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=get_post|list_posts|get_post_metrics|create_comment,default=get_post"`

	Slug string `json:"slug" schema:"label=Post Slug,type=text,help=Product slug. At least one of slug or id is required.,depends_on_key=operation,depends_on_values=get_post|get_post_metrics"`

	ID string `json:"id" schema:"label=Post ID,type=text,help=Product ID. At least one of slug or id is required.,depends_on_key=operation,depends_on_values=get_post|get_post_metrics"`

	Order string `json:"order" schema:"label=Order,type=select,options=RANKING|NEWEST,default=RANKING,depends_on_key=operation,depends_on_values=list_posts"`

	First float64 `json:"first" schema:"label=First,type=number,default=10,depends_on_key=operation,depends_on_values=list_posts"`

	Topic string `json:"topic" schema:"label=Topic Slug,type=text,depends_on_key=operation,depends_on_values=list_posts"`

	PostID string `json:"post_id" schema:"label=Post ID,type=text,depends_on_key=operation,depends_on_values=create_comment"`

	Body string `json:"body" schema:"label=Comment,type=textarea,rows=3,depends_on_key=operation,depends_on_values=create_comment"`

	AccessToken string `json:"access_token" schema:"label=Developer Token,type=text,help=OAuth2 or developer token. Resolved from the saved connection when omitted."`
}
