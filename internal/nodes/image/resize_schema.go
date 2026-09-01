package image

// ImageResizeNodeSchema documents the config keys ImageResizeNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type ImageResizeNodeSchema struct {
	Field string `json:"field" schema:"label=Image Path Field,type=text,help=Item field holding the local image path. Defaults to image_path， path， file_path， or media_path (first non-empty)."`

	Width float64 `json:"width" schema:"label=Width (px),type=number,help=Target width. At least one of width or height is required."`

	Height float64 `json:"height" schema:"label=Height (px),type=number,help=Target height. At least one of width or height is required."`

	Fit string `json:"fit" schema:"label=Fit Mode,type=select,options=contain|cover|fill|width|height,default=contain,help=contain: fit within bounds keeping aspect ratio； cover: fill and center-crop； fill: stretch to exact size； width/height: set only that dimension."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,default=image_path,help=Item field where the output file path is written."`

	OutputDir string `json:"output_dir" schema:"label=Output Directory,type=text,help=Directory for the resized file. Defaults to the source image's directory. ~ is expanded."`
}
