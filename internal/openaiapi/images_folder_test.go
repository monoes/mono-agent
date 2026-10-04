package openaiapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Every turn is told a folder of its own to save its images in, and one nobody could
// have known before: what is read afterwards is read from there and nowhere else.
func TestImagesEveryTurnIsToldAFolderOfItsOwn(t *testing.T) {
	log := &execLog{}
	h := newHarness(t, log.imageExec("a.png", onePNG("a.png")))
	secret := h.key(t, "default", "app", false)
	for range 3 {
		if rec := postImages(h, anyPolicy, secret, `{"prompt":"x"}`); rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	seen := map[string]bool{}
	for _, o := range log.opts {
		m := imageFolderRE.FindStringSubmatch(o.SystemPrompt)
		if m == nil {
			t.Fatalf("the system prompt names no output folder: %q", o.SystemPrompt)
		}
		if seen[m[1]] {
			t.Errorf("two turns were told the same folder, %s", m[1])
		}
		seen[m[1]] = true
	}
}

// A runtime that does not save where it was told has made nothing the gateway reads:
// what else is in its working folder is not returned, and the operator's log says it was
// there.
func TestImagesAFileOutsideTheTurnsFolderIsNotReturned(t *testing.T) {
	h := newHarness(t, imageTurnTop("saved image.png", map[string][]byte{"image.png": pngBytes}))
	rec := postImages(h, anyPolicy, h.key(t, "default", "app", false), `{"prompt":"x"}`)
	if rec.Code != http.StatusBadGateway || decodeErrorBody(t, rec)["code"] != "image_generation_failed" {
		t.Fatalf("a runtime that saved at the top of its working folder: %d %s", rec.Code, rec.Body.String()[:min(rec.Body.Len(), 300)])
	}
	if strings.Contains(rec.Body.String(), base64.StdEncoding.EncodeToString(pngBytes)[:16]) {
		t.Error("the answer carries a file that was not in the turn's folder")
	}
	if lines := strings.Join(h.logged(), "\n"); !strings.Contains(lines, "status=502") || !strings.Contains(lines, "outside the turn's folder x1") {
		t.Errorf("the log must say that a file was saved outside the turn's folder: %q", lines)
	}

	// Next to an image in the turn's folder, it is left out, and the folder's image is the answer.
	both := func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		if err := os.WriteFile(filepath.Join(o.Cwd, "0-top.png"), pngBytes, 0o600); err != nil {
			return nil, err
		}
		return imageTurn("in.png", map[string][]byte{"in.png": jpegBytes})(ctx, o, onEvent)
	}
	h = newHarness(t, both)
	rec = postImages(h, anyPolicy, h.key(t, "default", "app", false), `{"prompt":"x","n":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, images := decodeImages(t, rec); !equalImages(images, jpegBytes) {
		t.Errorf("got %d images, want only the one in the turn's folder", len(images))
	}
	if lines := strings.Join(h.logged(), "\n"); !strings.Contains(lines, "status=200") || !strings.Contains(lines, "outside the turn's folder x1") {
		t.Errorf("a success that left a file outside the folder says so: %q", lines)
	}
}
