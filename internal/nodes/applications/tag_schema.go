// internal/nodes/applications/tag_schema.go
package applicationsnodes

// TagNodeSchema documents the config keys TagNode.Execute reads out of its
// map[string]interface{} config.
type TagNodeSchema struct {
	ID        string `json:"id" schema:"label=Application ID,type=text,required,help=The application to tag."`
	Tag       string `json:"tag" schema:"label=Tag,type=text,required,help=The tag to add or remove."`
	Action    string `json:"action" schema:"label=Action,type=select,default=add,options=add|remove,help=Whether to add or remove the tag."`
	ProfileID string `json:"profile_id" schema:"label=Profile ID,type=text,default=default,help=Which profile owns this application."`
}
