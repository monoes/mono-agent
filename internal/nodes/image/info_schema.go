package image

// ImageInfoNodeSchema documents the config keys ImageInfoNode.Execute reads
// out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type ImageInfoNodeSchema struct {
	Field string `json:"field" schema:"label=Image Path Field,type=text,help=Item field holding the local image path. Defaults to image_path， path， file_path， or media_path (first non-empty)."`
}
