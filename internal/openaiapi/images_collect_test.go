package openaiapi

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What a runtime's image looks like as far as the collection can tell: the first
// bytes decide, not the name.
var (
	pngBytes  = []byte("\x89PNG\r\n\x1a\nnot really a picture")
	jpegBytes = []byte("\xff\xd8\xff\xe0not really a picture")
	gifBytes  = []byte("GIF89anot really a picture")
	webpBytes = []byte("RIFF\x1a\x00\x00\x00WEBPVP8 not really a picture")
)

// slotFolder is an empty folder that stands for a turn's: <temp>/slot-0.
func slotFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "slot-0")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFiles(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func equalImages(got [][]byte, want ...[]byte) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !bytes.Equal(got[i], want[i]) {
			return false
		}
	}
	return true
}

// The first bytes decide what is an image: PNG, JPEG, WebP and GIF, whatever the
// file is called, and nothing else.
func TestCollectImagesTakesPNGJPEGWebPAndGIFByTheirFirstBytes(t *testing.T) {
	dir := slotFolder(t)
	writeFiles(t, dir, map[string][]byte{
		"a.png": pngBytes, "b.jpg": jpegBytes, "c.webp": webpBytes, "d.gif": gifBytes,
		"e.txt":      []byte("a note, not a picture"),
		"f.png":      []byte("a text file that is named like a picture"),
		"g.bmp":      []byte("BM bitmaps are not one of the four"),
		"h.svg":      []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"),
		"i.riff":     []byte("RIFF\x1a\x00\x00\x00WAVEfmt  a wave file is RIFF too"),
		"j.short":    []byte("GIF"),
		"k.jpeg.png": []byte("\xff\xd8 two bytes of a JPEG are not enough"),
	})

	got := collectImages(dir, 4)
	if !equalImages(got.images, pngBytes, jpegBytes, webpBytes, gifBytes) {
		t.Fatalf("collected %d images, want the PNG, the JPEG, the WebP and the GIF in name order", len(got.images))
	}
	// With room for more, only a file that is an image by its bytes follows, whatever its name.
	writeFiles(t, dir, map[string][]byte{"z.data": pngBytes})
	if got := collectImages(dir, 10); !equalImages(got.images, pngBytes, jpegBytes, webpBytes, gifBytes, pngBytes) {
		t.Fatalf("collected %d images, want the four and z.data: the name does not decide", len(got.images))
	}
}

func TestIsImageKnowsTheFourSignatures(t *testing.T) {
	for name, c := range map[string]struct {
		b    []byte
		want bool
	}{
		"png":                     {[]byte("\x89PNG\r\n\x1a\n"), true},
		"jpeg":                    {[]byte("\xff\xd8\xff"), true},
		"gif87a":                  {[]byte("GIF87a"), true},
		"gif89a":                  {[]byte("GIF89a"), true},
		"webp":                    {[]byte("RIFF\x00\x00\x00\x00WEBP"), true},
		"empty":                   {nil, false},
		"a damaged png signature": {[]byte("\x89PNG\r\n\x1b\n"), false},
		"two bytes of a jpeg":     {[]byte("\xff\xd8"), false},
		"gif88a":                  {[]byte("GIF88a"), false},
		"a wave":                  {[]byte("RIFF\x00\x00\x00\x00WAVE"), false},
		"a truncated webp":        {[]byte("RIFF\x00\x00\x00\x00WEB"), false},
		"webp not at the start":   {[]byte(" RIFF\x00\x00\x00\x00WEBP"), false},
	} {
		if got := isImage(c.b); got != c.want {
			t.Errorf("%s: isImage = %v, want %v", name, got, c.want)
		}
	}
}

// At most n are taken, the first n by name, so that the same turn gives the same
// answer.
func TestCollectImagesKeepsTheFirstNByName(t *testing.T) {
	dir := slotFolder(t)
	one, two, three := append([]byte("\x89PNG\r\n\x1a\n"), 1), append([]byte("\x89PNG\r\n\x1a\n"), 2), append([]byte("\x89PNG\r\n\x1a\n"), 3)
	writeFiles(t, dir, map[string][]byte{"img-3.png": three, "img-1.png": one, "img-2.png": two})
	if got := collectImages(dir, 2); !equalImages(got.images, one, two) {
		t.Fatalf("n=2 gave %d images, want img-1 and img-2", len(got.images))
	}
	if got := collectImages(dir, 1); !equalImages(got.images, one) {
		t.Fatalf("n=1 gave %d images, want img-1", len(got.images))
	}
	if got := collectImages(dir, 4); len(got.images) != 3 {
		t.Fatalf("n=4 over three files gave %d images, want the three there are", len(got.images))
	}
}

// One level only: what is in a folder of the turn's folder is not collected, the
// turn's own temp folder included, and a directory named like a picture is not one.
func TestCollectImagesReadsOneLevelOnly(t *testing.T) {
	dir := slotFolder(t)
	for _, d := range []string{"sub", ".tmp", "looks-like.png"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFiles(t, filepath.Join(dir, d), map[string][]byte{"inner.png": pngBytes})
	}
	if got := collectImages(dir, 4); len(got.images) != 0 || len(got.skipped) != 0 {
		t.Fatalf("collected %d images from folders of the turn's folder, and left out %v: a folder is not a file that was left out", len(got.images), got.skipped)
	}
	writeFiles(t, dir, map[string][]byte{"top.png": jpegBytes})
	if got := collectImages(dir, 4); !equalImages(got.images, jpegBytes) {
		t.Fatalf("the file at the top level was not the only one collected: %d images", len(got.images))
	}
}

// 20 MiB is the most one image may weigh: that is taken, one byte more is not, and
// is not read.
func TestCollectImagesTakesAtMost20MiBPerImage(t *testing.T) {
	dir := slotFolder(t)
	big := func(name string, size int64) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(pngBytes); err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil { // sparse: the rest is zeros
			t.Fatal(err)
		}
		f.Close()
	}
	big("a-exact.png", maxImageBytes)
	big("b-over.png", maxImageBytes+1)

	got := collectImages(dir, 4)
	if len(got.images) != 1 || len(got.images[0]) != maxImageBytes {
		t.Fatalf("collected %d images, want only the one of exactly 20 MiB", len(got.images))
	}
	if got.skipped["too large"] != 1 {
		t.Errorf("skipped = %v, want one file too large", got.skipped)
	}
}

// A file can grow between the look at its size and the read, and a process a runtime
// left behind can keep it growing: no more than 20 MiB is ever read.
func TestCollectImagesDoesNotReadPastTheLimitWhenAFileGrows(t *testing.T) {
	dir := slotFolder(t)
	path := filepath.Join(dir, "a.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(pngBytes); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxImageBytes); err != nil {
		t.Fatal(err)
	}
	f.Close()
	afterImageLstatHook.set(func() { // the size looked at was the limit; now it is more
		g, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer g.Close()
		_, _ = g.Write([]byte("more"))
	})
	t.Cleanup(func() { afterImageLstatHook.set(nil) })

	got := collectImages(dir, 4)
	if len(got.images) != 0 || got.skipped["too large"] != 1 {
		t.Fatalf("a file that grew past 20 MiB: %d images, skipped %v", len(got.images), got.skipped)
	}
}

// A folder that cannot be opened (it is not there, or it is not a plain folder)
// gives nothing and says so.
func TestCollectImagesOfAFolderThatIsNotThere(t *testing.T) {
	got := collectImages(filepath.Join(t.TempDir(), "missing"), 4)
	if len(got.images) != 0 || got.note() == "" {
		t.Errorf("a missing folder: %d images, note %q", len(got.images), got.note())
	}
}

// What was left out is told to the operator by reason and count, never by name:
// a runtime chooses the names.
func TestCollectedNoteCountsByReasonAndNamesNothing(t *testing.T) {
	c := collected{skipped: map[string]int{"too large": 1, "a link": 2, "not an image": 3}}
	if got, want := c.note(), "a link x2, not an image x3, too large x1"; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	if (collected{}).note() != "" {
		t.Error("nothing was left out, so there is nothing to say")
	}
	dir := slotFolder(t)
	writeFiles(t, dir, map[string][]byte{"secret-name.txt": []byte("not an image")})
	if note := collectImages(dir, 1).note(); strings.Contains(note, "secret-name") || note == "" {
		t.Errorf("note = %q: it must count the file by reason and not name it", note)
	}
}
