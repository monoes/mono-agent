package service

// YouTubeNodeSchema documents the config keys YouTubeNode.Execute reads out
// of its map[string]interface{} config — see internal/nodes/control/set_schema.go's
// doc comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
//
// "search_videos" has no entry in the hand-written
// schemas/service.youtube.json this replaces — Execute has supported it
// since it was written (see the "search_videos" case, which reads "query"
// and "max_results"), but the schema's operation options list never
// included it and neither "query" nor "max_results" had a schema field.
// Generating from the struct surfaces that gap; all three are added below.
//
// credential_platform: youtube
type YouTubeNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=YouTube Connection,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=upload_video|get_video_stats|list_comments|reply_to_comment|search_videos,default=upload_video"`

	Title string `json:"title" schema:"label=Title,type=text,depends_on_key=operation,depends_on_values=upload_video"`

	Description string `json:"description" schema:"label=Description,type=textarea,rows=4,depends_on_key=operation,depends_on_values=upload_video"`

	Tags []string `json:"tags" schema:"label=Tags,type=tag_list,depends_on_key=operation,depends_on_values=upload_video"`

	CategoryID string `json:"category_id" schema:"label=Category ID,type=text,default=22,depends_on_key=operation,depends_on_values=upload_video"`

	PrivacyStatus string `json:"privacy_status" schema:"label=Privacy Status,type=select,options=public|unlisted|private,default=public,depends_on_key=operation,depends_on_values=upload_video"`

	VideoFilePath string `json:"video_file_path" schema:"label=Video File Path,type=text,depends_on_key=operation,depends_on_values=upload_video"`

	VideoID string `json:"video_id" schema:"label=Video ID,type=text,depends_on_key=operation,depends_on_values=get_video_stats|list_comments"`

	CommentID string `json:"comment_id" schema:"label=Comment ID,type=text,depends_on_key=operation,depends_on_values=reply_to_comment"`

	Text string `json:"text" schema:"label=Reply Text,type=textarea,rows=3,depends_on_key=operation,depends_on_values=reply_to_comment"`

	Query string `json:"query" schema:"label=Search Query,type=text,depends_on_key=operation,depends_on_values=search_videos"`

	MaxResults float64 `json:"max_results" schema:"label=Max Results,type=number,default=10,depends_on_key=operation,depends_on_values=search_videos"`
}
