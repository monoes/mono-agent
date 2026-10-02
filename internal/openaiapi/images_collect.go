package openaiapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
)

// maxImageBytes is the most one image of a response may weigh.
const maxImageBytes = 20 << 20

// maxCollectEntries is how many entries of a folder the collection looks at. A turn
// makes at most four images; a folder with more than this is a mess, or the work of a
// process that outlived an earlier turn, and listing and looking at every entry of one
// is a cost it can choose.
const maxCollectEntries = 64

// collectBudget is how long the collection may take, whatever the folder holds and
// whatever a step of it waits for. A variable so a test can shorten it.
var collectBudget = time.Minute

// afterImageLstatHook runs after the collection has looked at a file and before it
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

// collectImages reads the image files a turn saved in its own output folder, subdir
// of its working folder dir: the first n by name. It is given up on when the caller
// leaves or collectBudget runs out, and then answers with nothing: a step of it that
// waits (on a disk, on a share) is not waited for, and ends by itself when it can, as
// emptyDir's walk does.
func collectImages(ctx context.Context, dir, subdir string, n int) collected {
	deadline := time.Now().Add(collectBudget)
	done := make(chan collected, 1) // buffered: a collection given up on must not wait for a reader of its answer
	go func() { done <- collectImagesBy(ctx, dir, subdir, n, deadline) }()
	timer := time.NewTimer(collectBudget)
	defer timer.Stop()
	select {
	case c := <-done:
		return c
	case <-ctx.Done():
		return collected{skipped: map[string]int{"the caller left": 1}}
	case <-timer.C:
		return collected{skipped: map[string]int{"it took too long": 1}}
	}
}

// collectImagesBy is the collection collectImages bounds. Both folders are opened as
// roots, each looked at from its parent, which no turn can change: the working
// folder, and the output folder in it, which the turn can swap for a link.
func collectImagesBy(ctx context.Context, dir, subdir string, n int, deadline time.Time) collected {
	slot, err := openFolder(dir)
	if err != nil {
		return collected{skipped: map[string]int{"the working folder could not be opened": 1}}
	}
	defer slot.Close()
	var out collected
	if sub, err := openChild(slot, subdir); err != nil {
		out.skip("the turn's own folder is gone or is not a plain folder")
	} else {
		out = readImages(ctx, sub, n, deadline)
		sub.Close()
	}
	out.countOutside(slot, subdir)
	return out
}

// countOutside counts the files at the top of the working folder, beside the output
// folder: whoever made them, they are not what the turn was told to make. Nothing in
// them is read. Without the count a runtime that saves where it was not told would
// look, in the log, like one that made nothing.
func (c *collected) countOutside(slot *os.Root, subdir string) {
	entries, _, err := readDirN(slot, maxCollectEntries)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type().IsRegular() && e.Name() != subdir {
			c.skip("outside the turn's folder")
		}
	}
}

// readDirN lists up to limit entries of the directory root is, in the order the system
// gives them, and says whether it holds more.
func readDirN(root *os.Root, limit int) ([]os.DirEntry, bool, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	entries, err := f.ReadDir(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) { // io.EOF: nothing there
		return nil, false, err
	}
	if len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

// readImages reads the image files at the top of root, the first n by name, out of the
// first maxCollectEntries entries. Only the top level is looked at and only regular
// files are read: what a runtime leaves there is not to be trusted, since it can be
// steered by a prompt, and the gateway runs as the OS user outside the sandbox the
// runtime has. So a link is never followed (a runtime could plant one to any image on
// the disk), a FIFO or a device is never opened, a file is read at most up to
// maxImageBytes, and everything is done through the handle of the folder, which refuses
// a path that leaves it. It stops at n images, when the caller has left, and at the
// deadline.
func readImages(ctx context.Context, root *os.Root, n int, deadline time.Time) collected {
	var out collected
	entries, more, err := readDirN(root, maxCollectEntries)
	if err != nil {
		out.skip("the folder could not be listed")
		return out
	}
	if more {
		out.skip("too many files")
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for _, e := range entries {
		if len(out.images) >= n {
			break
		}
		if ctx.Err() != nil {
			out.skip("the caller left")
			break
		}
		if expired(deadline) {
			out.skip("it took too long")
			break
		}
		if e.IsDir() { // none holds an image of the turn's
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

// readImage reads one file of the folder, or says why it was left out. What a file
// begins with decides whether it is read any further: a file that is no image costs
// the few bytes that say so, whatever its size.
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
	var head [12]byte // the longest signature: RIFF, a size, WEBP
	k, err := io.ReadFull(f, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) { // a file shorter than that is all there is
		return nil, "unreadable"
	}
	if !isImage(head[:k]) {
		return nil, "not an image"
	}
	var buf bytes.Buffer
	buf.Grow(int(fi.Size()) + 1)
	buf.Write(head[:k])
	if _, err := buf.ReadFrom(io.LimitReader(f, maxImageBytes+1-int64(k))); err != nil {
		return nil, "unreadable"
	}
	if buf.Len() > maxImageBytes { // it grew since it was looked at
		return nil, "too large"
	}
	return buf.Bytes(), ""
}
