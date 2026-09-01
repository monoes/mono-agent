package image

// ImageAdjustNodeSchema documents the config keys ImageAdjustNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control.SetNodeSchema's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
type ImageAdjustNodeSchema struct {
	Field string `json:"field" schema:"label=Image Path Field,type=text,help=Item field holding the local image path. Defaults to image_path， path， file_path， or media_path (first non-empty)."`

	Brightness float64 `json:"brightness" schema:"label=Brightness,type=number,default=0,min=-100,max=100,help=Brightness adjustment (-100 to 100). 0 leaves it unchanged."`

	Contrast float64 `json:"contrast" schema:"label=Contrast,type=number,default=0,min=-100,max=100,help=Contrast adjustment (-100 to 100). 0 leaves it unchanged."`

	Saturation float64 `json:"saturation" schema:"label=Saturation,type=number,default=0,min=-100,max=100,help=Saturation adjustment (-100 to 100). 0 leaves it unchanged."`

	Gamma float64 `json:"gamma" schema:"label=Gamma,type=number,default=1.0,help=Gamma correction. >1 brightens， <1 darkens， 1.0 leaves it unchanged."`

	Sharpen float64 `json:"sharpen" schema:"label=Sharpen (sigma px),type=number,default=0,help=Sharpening radius in pixels. 0 disables sharpening."`

	Blur float64 `json:"blur" schema:"label=Blur (sigma px),type=number,default=0,help=Gaussian blur sigma in pixels. 0 disables blur."`

	Grayscale bool `json:"grayscale" schema:"label=Grayscale,type=boolean,default=false,help=Convert the image to grayscale."`

	Sepia bool `json:"sepia" schema:"label=Sepia,type=boolean,default=false,help=Apply a sepia tone."`

	Invert bool `json:"invert" schema:"label=Invert Colors,type=boolean,default=false,help=Invert all colors."`

	OutputField string `json:"output_field" schema:"label=Output Field,type=text,default=image_path,help=Item field where the output file path is written."`

	OutputDir string `json:"output_dir" schema:"label=Output Directory,type=text,help=Directory for the adjusted file. Defaults to the source image's directory. ~ is expanded."`
}
