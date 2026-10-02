package openaiapi

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"syscall"
)

// maxImageBytes is the most one image of a response may weigh.
const maxImageBytes = 20 << 20

// afterImageLstatHook runs after collectImages has looked at a file and before it
// opens it: a test changes the file there, as a process that outlived its turn
// could.
var afterImageLstatHook testHook

// collected is what the collection found in a turn's folder.
type collected struct {
	images [][]byte
	// skipped counts the files left out by the reason, never by name: a runtime
	// chooses the names, and they belong to the operator's log at most.
	skipped map[string]int
}

func (c *collected) skip(why string) {
	if c.skipped == nil {
		c.skipped = map[string]int{}
	}
	c.skipped[why]++
}

// note says what was left out, by reason and count, for the operator's log.
func (c collected) note() string {
	parts := make([]string, 0, len(c.skipped))
	for _, why := range slices.Sorted(maps.Keys(c.skipped)) {
		parts = append(parts, fmt.Sprintf("%s x%d", why, c.skipped[why]))
	}
	return strings.Join(parts, ", ")
}

// isImage reports whether b starts like a PNG, a JPEG, a WebP or a GIF: the
// bytes decide, not a file's name or what the runtime said it made.
func isImage(b []byte) bool {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return true
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return true
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return true
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return true
	}
	return false
}

// collectImages reads the image files a turn left in its folder, the first n by
// name. Only the top level is looked at and only regular files are read: what a
// runtime leaves there is not to be trusted, since it can be steered by a prompt,
// and the gateway runs as the OS user outside the sandbox the runtime has. So a
// link is never followed (a runtime could plant one to any image on the disk), a
// FIFO or a device is never opened, a file is read at most up to maxImageBytes, and
// everything is done through the handle of the folder (openFolder), which refuses
// a path that leaves it. It reads nothing it will not keep: it stops at n images.
func collectImages(dir string, n int) collected {
	var out collected
	root, err := openFolder(dir)
	if err != nil {
		out.skip("the folder could not be opened")
		return out
	}
	defer root.Close()
	entries, err := readDir(root)
	if err != nil {
		out.skip("the folder could not be listed")
		return out
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, e := range entries {
		if len(out.images) >= n {
			break
		}
		if e.IsDir() { // the turn's own temp folder is one, and none holds an image of the turn's
			continue
		}
		img, why := readImage(root, e.Name())
		if why != "" {
			out.skip(why)
			continue
		}
		out.images = append(out.images, img)
	}
	return out
}

// readImage reads one file of the folder, or says why it was left out.
func readImage(root *os.Root, name string) ([]byte, string) {
	fi, err := root.Lstat(name) // a link is not followed
	switch {
	case err != nil:
		return nil, "unreadable"
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, "a link"
	case !fi.Mode().IsRegular():
		return nil, "not a regular file"
	case fi.Size() > maxImageBytes:
		return nil, "too large"
	}
	afterImageLstatHook.run()
	// O_NONBLOCK: whatever stands at the name now, a FIFO included, is opened at
	// once instead of after a writer comes. It is looked at before it is read.
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, "unreadable"
	}
	defer f.Close()
	if cur, err := f.Stat(); err != nil || !cur.Mode().IsRegular() || !os.SameFile(fi, cur) {
		return nil, "changed while it was read"
	}
	var buf bytes.Buffer
	buf.Grow(int(fi.Size()) + 1)
	if _, err := buf.ReadFrom(io.LimitReader(f, maxImageBytes+1)); err != nil {
		return nil, "unreadable"
	}
	if buf.Len() > maxImageBytes { // it grew since it was looked at
		return nil, "too large"
	}
	if !isImage(buf.Bytes()) {
		return nil, "not an image"
	}
	return buf.Bytes(), ""
}
