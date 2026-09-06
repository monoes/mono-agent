package image

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePNGChunk appends one PNG chunk (length + type + data + CRC) to buf.
func writePNGChunk(buf *bytes.Buffer, typ string, data []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	buf.Write(lenBuf[:])
	buf.WriteString(typ)
	buf.Write(data)

	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())
	buf.Write(crcBuf[:])
}

// writeBombPNG hand-constructs a minimal PNG file that declares an enormous
// width/height in its IHDR chunk but carries no real pixel data. Decoding
// the PNG header (image.DecodeConfig) only needs the IHDR chunk, so this is
// enough to exercise the pixel-count guard without needing gigabytes of
// actual (even compressed) pixel data on disk.
func writeBombPNG(t *testing.T, width, height uint32) string {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8  // bit depth
	ihdr[9] = 2  // color type: truecolor
	ihdr[10] = 0 // compression method
	ihdr[11] = 0 // filter method
	ihdr[12] = 0 // interlace method
	writePNGChunk(&buf, "IHDR", ihdr)

	p := filepath.Join(t.TempDir(), "bomb.png")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write test png: %v", err)
	}
	return p
}

// writeTinyPNG writes a real, fully valid, tiny PNG file and returns its path.
func writeTinyPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 60), G: uint8(y * 60), B: 100, A: 255})
		}
	}
	p := filepath.Join(t.TempDir(), "tiny.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create test png: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return p
}

// TestOpenImageSafely_RejectsDecompressionBomb is the regression test for
// Finding 3: a file that declares enormous dimensions in its header must be
// rejected before imaging/image.Decode ever allocates a pixel buffer sized
// from that (attacker-controlled) header.
func TestOpenImageSafely_RejectsDecompressionBomb(t *testing.T) {
	// 50,000 x 50,000 = 2.5 billion declared pixels, far past maxImagePixels,
	// while the file on disk is only a few dozen bytes.
	path := writeBombPNG(t, 50000, 50000)

	_, err := openImageSafely(path)
	if err == nil {
		t.Fatal("openImageSafely() = nil error, want error for oversized declared dimensions")
	}
	if !strings.Contains(err.Error(), "pixel limit") {
		t.Errorf("openImageSafely() error = %v, want a pixel-limit error", err)
	}
}

// TestOpenImageSafely_AllowsNormalImage ensures the guard does not reject
// ordinary, small images.
func TestOpenImageSafely_AllowsNormalImage(t *testing.T) {
	path := writeTinyPNG(t)

	img, err := openImageSafely(path)
	if err != nil {
		t.Fatalf("openImageSafely() error = %v, want nil", err)
	}
	b := img.Bounds()
	if b.Dx() != 4 || b.Dy() != 4 {
		t.Errorf("openImageSafely() image bounds = %v, want 4x4", b)
	}
}

// TestOpenImageSafely_MissingFile ensures a normal not-found error still
// surfaces cleanly (rather than a nil pointer panic) once os.Open is
// interposed ahead of the imaging.Open call.
func TestOpenImageSafely_MissingFile(t *testing.T) {
	_, err := openImageSafely(filepath.Join(t.TempDir(), "does-not-exist.png"))
	if err == nil {
		t.Fatal("openImageSafely() = nil error, want error for missing file")
	}
}
