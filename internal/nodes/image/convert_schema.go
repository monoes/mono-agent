package image

// ImageConvertNodeSchema documents the config keys ImageConvertNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type ImageConvertNodeSchema struct {
	Field string `json:"field" schema:"label=Image Path Field,type=text,help=Item field holding the local image path. Defaults to image_path， path， file_path， or media_path (first non-empty)."`

	Format string `json:"format" schema:"label=Target Format,type=select,required,options=jpeg|png|gif|tiff|bmp,help=Output image format. jpg is accepted as an alias for jpeg； webp can be decoded but not written."`

	Quality float64 `json:"quality" schema:"label=JPEG Quality,type=number,default=85,min=1,max=100,help=JPEG encode quality (only used when format is jpeg)."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,default=image_path,help=Item field where the output file path is written."`

	OutputDir string `json:"output_dir" schema:"label=Output Directory,type=text,help=Directory for the converted file. Defaults to the source image's directory. ~ is expanded."`
}
