package image

// ImageCropNodeSchema documents the config keys ImageCropNode.Execute reads
// out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type ImageCropNodeSchema struct {
	Field string `json:"field" schema:"label=Image Path Field,type=text,help=Item field holding the local image path. Defaults to image_path， path， file_path， or media_path (first non-empty)."`

	X float64 `json:"x" schema:"label=Crop X (px),type=number,help=Left edge of the crop region. Requires y， width， and height."`

	Y float64 `json:"y" schema:"label=Crop Y (px),type=number,help=Top edge of the crop region. Requires x， width， and height."`

	Width float64 `json:"width" schema:"label=Crop Width (px),type=number,help=Width of the crop region (explicit region mode)."`

	Height float64 `json:"height" schema:"label=Crop Height (px),type=number,help=Height of the crop region (explicit region mode)."`

	AspectRatio string `json:"aspect_ratio" schema:"label=Aspect Ratio,type=text,placeholder=e.g. 1:1， 16:9， 4:3,help=Crop to this aspect ratio instead of an explicit region. Provide either (x， y， width， height) or aspect_ratio."`

	Anchor string `json:"anchor" schema:"label=Anchor,type=select,options=center|top|bottom|left|right|topleft|topright,default=center,help=Which part of the image to keep when cropping by aspect ratio."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,default=image_path,help=Item field where the output file path is written."`

	OutputDir string `json:"output_dir" schema:"label=Output Directory,type=text,help=Directory for the cropped file. Defaults to the source image's directory. ~ is expanded."`
}
