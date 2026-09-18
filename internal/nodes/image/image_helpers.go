package image

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/monoes/mono-agent/internal/fsconfine"
)

// resolveImageField extracts a file path from an item field (or falls back to
// "image_path", "path", "file_path", "media_path" in that order).
func resolveImageField(json map[string]interface{}, field string) string {
	if field != "" {
		if v, ok := json[field].(string); ok && v != "" {
			return v
		}
	}
	for _, k := range []string{"image_path", "path", "file_path", "media_path", "uploaded"} {
		if v, ok := json[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		return filepath.Join(home, p[2:])
	}
	return p
}

func intConfig(config map[string]interface{}, key string, def int) int {
	switch v := config[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func floatConfig(config map[string]interface{}, key string, def float64) float64 {
	switch v := config[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return def
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// maxImagePixels bounds the declared width*height of any image opened by
// this package. image.Decode allocates a full pixel buffer sized from the
// file's own header before validating the actual content, so a tiny file
// that declares enormous dimensions can force a multi-gigabyte allocation
// (a "decompression bomb" DoS). image_path values commonly originate from
// files just downloaded from untrusted remote sources (e.g. by the http
// node), so this guard runs on every path opened here. 100 megapixels is
// generous for any real photo (a 24MP DSLR shot is ~6000x4000) while staying
// well below a threshold that could exhaust memory.
const maxImagePixels = 100_000_000

// openImageSafely opens the image at path after first checking its declared
// dimensions via image.DecodeConfig, which only reads the file header
// rather than decoding and allocating the full pixel buffer. This is the
// shared entry point all Execute methods in this file must use instead of
// calling imaging.Open directly, so the pixel-count guard cannot be
// accidentally skipped at a new call site.
func openImageSafely(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	cfg, _, err := image.DecodeConfig(f)
	closeErr := f.Close()
	if err != nil {
		return nil, fmt.Errorf("read image header %q: %w", path, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close %q: %w", path, closeErr)
	}

	pixels := int64(cfg.Width) * int64(cfg.Height)
	if pixels > maxImagePixels {
		return nil, fmt.Errorf("image %q declares %dx%d pixels (%d), which exceeds the %d pixel limit", path, cfg.Width, cfg.Height, pixels, maxImagePixels)
	}

	return imaging.Open(path, imaging.AutoOrientation(true))
}

// imagePath resolves the image path an item carries (see resolveImageField)
// and confines it to the org workdir when the run has one (C-46). An item
// without a path yields "".
func imagePath(ctx context.Context, item map[string]interface{}, field string) (string, error) {
	p := expandHome(resolveImageField(item, field))
	if p == "" {
		return "", nil
	}
	return fsconfine.Path(ctx, p)
}

// buildOutputPath creates the output file path, optionally in a different
// directory, confined to the org workdir when the run has one. The output
// directory is created only after the path passed that check.
func buildOutputPath(ctx context.Context, inputPath, outputDir, suffix, ext string) (string, error) {
	dir := filepath.Dir(inputPath)
	if outputDir != "" {
		dir = expandHome(outputDir)
	}
	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	if ext == "" {
		ext = strings.TrimPrefix(filepath.Ext(inputPath), ".")
	}
	ext = strings.TrimPrefix(ext, ".")
	ts := fmt.Sprintf("%d", time.Now().UnixNano()/1e6)
	out, err := fsconfine.Path(ctx, filepath.Join(dir, fmt.Sprintf("%s_%s_%s.%s", base, suffix, ts, ext)))
	if err != nil {
		return "", err
	}
	if outputDir != "" {
		_ = os.MkdirAll(filepath.Dir(out), 0750)
	}
	return out, nil
}
