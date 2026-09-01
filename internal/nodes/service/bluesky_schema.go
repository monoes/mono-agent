package service

// BlueskyNodeSchema documents the config keys BlueskyNode.Execute reads out
// of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. It is never constructed or
// used at runtime; this struct exists solely to generate
// internal/workflow/schemas/service.bluesky.json.
//
// credential_platform: bluesky
type BlueskyNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=Bluesky Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=create_post|get_timeline|get_profile|like_post|repost,default=create_post"`

	Text string `json:"text" schema:"label=Post Text,type=textarea,rows=4,depends_on_key=operation,depends_on_values=create_post"`

	Limit float64 `json:"limit" schema:"label=Limit,type=number,default=30,depends_on_key=operation,depends_on_values=get_timeline"`

	Actor string `json:"actor" schema:"label=Actor,type=text,help=Handle or DID to fetch.,depends_on_key=operation,depends_on_values=get_profile"`

	URI string `json:"uri" schema:"label=Post URI (at://...),type=text,help=Subject record URI.,depends_on_key=operation,depends_on_values=like_post|repost"`

	CID string `json:"cid" schema:"label=Post CID,type=text,help=Subject record CID.,depends_on_key=operation,depends_on_values=like_post|repost"`

	Identifier string `json:"identifier" schema:"label=Handle,type=text,help=Bluesky handle. Resolved from the saved connection when omitted."`

	AppPassword string `json:"app_password" schema:"label=App Password,type=text,help=App password from Bluesky settings. Resolved from the saved connection when omitted."`
}
