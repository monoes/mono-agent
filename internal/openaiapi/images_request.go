package openaiapi

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

const (
	// maxImages is the most images one request may ask for.
	maxImages = 4
	// minImageSide and maxImageSide bound each side of a preferred size, in pixels.
	minImageSide = 64
	maxImageSide = 8192
	// maxImageBody is the largest body of an image request: a prompt, not a
	// document (spec §6.4). The configured body limit applies when it is smaller.
	maxImageBody = 64 << 10
)

// ImageRequest is the part of POST /v1/images/generations this server reads.
// quality, style, output_format, background, user and whatever else a client
// sends are accepted by the decoder and ignored: the runtime decides them.
type ImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              *int   `json:"n"`
	Size           string `json:"size"`
	ResponseFormat string `json:"response_format"`
	Stream         bool   `json:"stream"`
}

// imageSizeRE is the shape of a size: two numbers of two to five digits, no sign,
// no leading zero, an x between them. Nothing else reaches a prompt.
var imageSizeRE = regexp.MustCompile(`^([1-9][0-9]{1,4})[xX]([1-9][0-9]{1,4})$`)

// imageTypeError is the answer to a field of the wrong JSON type: which field, and
// what it has to be. The client's own value is not echoed.
func imageTypeError(field string, want reflect.Type) *apiError {
	var what string
	switch want.Kind() {
	case reflect.Int:
		what = "a whole number"
	case reflect.Bool:
		what = "true or false"
	default:
		what = "a string"
	}
	return errInvalid("invalid_value", field, field+" must be "+what)
}

// validateImages checks an image request and returns what the turn needs of it:
// how many images (1 when the client did not say) and the preferred size as WxH,
// "" for none. The client's own words are never echoed in an error.
func validateImages(req *ImageRequest) (n int, size string, e *apiError) {
	switch {
	case req.Prompt == "":
		return 0, "", errInvalid("missing_required_parameter", "prompt", "prompt is required")
	case strings.TrimSpace(req.Prompt) == "":
		return 0, "", errInvalid("invalid_value", "prompt", "prompt must not be blank")
	}
	n = 1
	if req.N != nil {
		if *req.N < 1 || *req.N > maxImages {
			return 0, "", errInvalid("invalid_value", "n", fmt.Sprintf("n must be a whole number from 1 to %d", maxImages))
		}
		n = *req.N
	}
	if req.Size != "" && req.Size != "auto" {
		w, h, ok := parseImageSize(req.Size)
		if !ok {
			return 0, "", errInvalid("invalid_value", "size", fmt.Sprintf(
				"size must be auto, or width x height in pixels such as 1024x1024, each from %d to %d", minImageSide, maxImageSide))
		}
		size = fmt.Sprintf("%dx%d", w, h)
	}
	switch req.ResponseFormat {
	case "", "b64_json":
	case "url":
		return 0, "", errUnsupported("response_format", "only response_format b64_json is supported: this server has no address to serve an image from")
	default:
		return 0, "", errInvalid("invalid_value", "response_format", "response_format must be b64_json")
	}
	if req.Stream {
		return 0, "", errUnsupported("stream", "streaming image generation is not supported")
	}
	return n, size, nil
}

// parseImageSize reads WxH, each side within the bounds.
func parseImageSize(s string) (w, h int, ok bool) {
	m := imageSizeRE.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	w, _ = strconv.Atoi(m[1])
	h, _ = strconv.Atoi(m[2])
	if w < minImageSide || w > maxImageSide || h < minImageSide || h > maxImageSide {
		return 0, 0, false
	}
	return w, h, true
}

// imagePrompt is what the turn is told: the client's prompt, then how many images
// and the size, when asked. n and size are validated: only a count and digits
// reach it.
func imagePrompt(prompt string, n int, size string) string {
	var asks []string
	if n > 1 {
		asks = append(asks, fmt.Sprintf("Create %d distinct images.", n))
	}
	if size != "" {
		asks = append(asks, "Preferred size: "+size+".")
	}
	if len(asks) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(asks, " ")
}

// imageSystemPrompt is the whole of what the runtime is told besides the client's
// prompt. The image is the runtime's own work: it is made by a tool the runtime has,
// saved in the folder it is given, a fresh one for every turn, and the gateway reads
// it from there and from nowhere else.
func imageSystemPrompt(folder string) string {
	return "You generate images for an API client.\n" +
		"Generate each requested image with your own built-in image generation capability. " +
		"Do not draw it programmatically: no code, scripts, SVG or plotting libraries.\n" +
		"Save each finished image as a file in the folder ./" + folder + "/, which exists already inside the current directory. " +
		"If your image tool saves it elsewhere, copy it into that folder (copy, do not link). Save nothing anywhere else.\n" +
		"When you are done, reply with the file names only, one per line.\n" +
		"If you have no built-in image generation capability, reply with exactly NO_IMAGE_TOOL on a line of its own and create no file."
}

// newImageFolder names the output folder of one turn: unpredictable, so that a process
// an earlier turn left running, which writes to the paths it knew, does not put its
// files where this turn's images are read from.
func newImageFolder() string { return newRequestID("out-") }

// noImageToolMarker is what the runtime replies when it cannot make an image.
const noImageToolMarker = "NO_IMAGE_TOOL"

// saidNoImageTool reports whether a reply says the runtime has no image tool: the
// marker as a line of its own, or as the whole reply. A sentence that holds the word
// does not say it, and neither does a file name that starts with it.
func saidNoImageTool(reply string) bool {
	for _, line := range strings.Split(reply, "\n") {
		if strings.TrimSpace(line) == noImageToolMarker {
			return true
		}
	}
	return false
}
