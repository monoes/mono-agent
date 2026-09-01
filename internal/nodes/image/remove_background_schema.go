package image

// RemoveBackgroundNodeSchema documents the config keys
// RemoveBackgroundNode.Execute reads out of its map[string]interface{}
// config (see remove_background_cgo.go — the !cgo build reads no config at
// all, it always errors) — see internal/nodes/control.SetNodeSchema's doc
// comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
type RemoveBackgroundNodeSchema struct {
	Field string `json:"field" schema:"label=Image Path Field,type=text,help=Item field holding the local image path. Defaults to image_path， path， file_path， or media_path (first non-empty)."`

	BgColor string `json:"bgcolor" schema:"label=Background Color,type=text,placeholder=e.g. #ffffff or fff,help=Flatten the transparent background onto this hex color instead of keeping alpha. Output is always PNG."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,default=image_path,help=Item field where the output file path is written."`

	OutputDir string `json:"output_dir" schema:"label=Output Directory,type=text,help=Directory for the output PNG. Defaults to the source image's directory. ~ is expanded."`
}
