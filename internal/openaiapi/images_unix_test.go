//go:build unix

package openaiapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// A runtime, or a prompt that steers it, can plant a link to an image anywhere on
// the disk. The gateway runs as the OS user, outside the sandbox: what the link
// points to is never read, however the request is made. Here end to end.
func TestImagesNeverAnswerWithWhatALinkPointsTo(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.png")
	secretBytes := []byte("\x89PNG\r\n\x1a\nthe contents of a file elsewhere on the disk")
	if err := os.WriteFile(secret, secretBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	plant := func(files map[string][]byte) execFunc {
		return func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
			if err := os.Symlink(secret, filepath.Join(o.Cwd, "a-image.png")); err != nil {
				return nil, err
			}
			if err := os.Symlink(outside, filepath.Join(o.Cwd, "b-folder")); err != nil {
				return nil, err
			}
			return imageTurn("a-image.png", files)(ctx, o, onEvent)
		}
	}

	// Only links: the runtime made nothing the gateway may return.
	h := newHarness(t, plant(nil))
	rec := postImages(h, anyPolicy, h.key(t, "default", "app", false), `{"prompt":"x"}`)
	if rec.Code != http.StatusBadGateway || decodeErrorBody(t, rec)["code"] != "image_generation_failed" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String()[:min(rec.Body.Len(), 300)])
	}
	if strings.Contains(rec.Body.String(), base64.StdEncoding.EncodeToString(secretBytes)[:16]) {
		t.Fatal("the answer carries a file a link pointed to")
	}
	if lines := strings.Join(h.logged(), "\n"); !strings.Contains(lines, "a link x2") {
		t.Errorf("the log must say that links were left out: %q", lines)
	}

	// Next to a real image, the links are left out and the image is returned.
	h = newHarness(t, plant(map[string][]byte{"c-real.png": jpegBytes}))
	rec = postImages(h, anyPolicy, h.key(t, "default", "app", false), `{"prompt":"x","n":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, images := decodeImages(t, rec); !equalImages(images, jpegBytes) {
		t.Fatalf("got %d images, want only the real one", len(images))
	}
	if got, err := os.ReadFile(secret); err != nil || string(got) != string(secretBytes) {
		t.Errorf("the file behind the link was changed: %v", err)
	}
}

// A FIFO standing where an image should be is never opened for reading, which
// would wait for a writer for ever: the request ends.
func TestImagesDoNotHangOnAFifo(t *testing.T) {
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		if err := syscall.Mkfifo(filepath.Join(o.Cwd, "image.png"), 0o600); err != nil {
			return nil, err
		}
		return imageTurn("image.png", nil)(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)

	done := make(chan *httpRecorder, 1)
	go func() { done <- postImages(h, anyPolicy, secret, `{"prompt":"x"}`) }()
	select {
	case rec := <-done:
		if rec.Code != http.StatusBadGateway || decodeErrorBody(t, rec)["code"] != "image_generation_failed" {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request hung on a FIFO")
	}
}
