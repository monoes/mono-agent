package image

// ImageThumbnailNodeSchema documents the config keys
// ImageThumbnailNode.Execute reads out of its map[string]interface{}
// config — see internal/nodes/control.SetNodeSchema's doc comment for why
// this is a companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type ImageThumbnailNodeSchema struct {
	Field string `json:"field" schema:"label=Image Path Field,type=text,help=Item field holding the local image path. Defaults to image_path， path， file_path， or media_path (first non-empty)."`

	Width float64 `json:"width" schema:"label=Width (px),type=number,required,help=Exact output width."`

	Height float64 `json:"height" schema:"label=Height (px),type=number,required,help=Exact output height."`

	Anchor string `json:"anchor" schema:"label=Anchor,type=select,options=center|top|bottom|left|right,default=center,help=Which part of the image to keep when center-cropping to the exact dimensions."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,default=image_path,help=Item field where the output file path is written."`

	OutputDir string `json:"output_dir" schema:"label=Output Directory,type=text,help=Directory for the thumbnail file. Defaults to the source image's directory. ~ is expanded."`
}
