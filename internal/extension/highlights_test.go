package extension

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// RCL-04's Go half is deliberately nothing: a highlight artifact is an
// ordinary envelope member, and the capture path carries it without knowing
// what it is. That claim is worth a test, because it rests on two things
// that could quietly stop being true — `highlights.json` passing
// ValidArtifactName's charset whitelist, and the assembler not filtering
// artifacts down to the four canonical names.

func TestHighlightsArtifactNameIsAccepted(t *testing.T) {
	if !capture.ValidArtifactName(HighlightsArtifact) {
		t.Fatalf("%q is not a valid artifact name — RCL-04 cannot ride the envelope", HighlightsArtifact)
	}
}

func TestCaptureCarriesHighlightsArtifact(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	highlights := `{"version":1,"url":"https://example.com/post","highlights":[
	  {"id":"hl-1","text":"torque the sprocket to 9 Nm",
	   "anchor":{"startChar":3200,"endChar":3226,"quote":"torque the sprocket to 9 Nm"},
	   "createdAt":"2026-09-21T10:00:00.000Z","comment":"check this on the bench"}]}`

	type outcome struct {
		res *capture.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := srv.CapturePage(CaptureRequest{TabID: 3, Timeout: 5 * time.Second})
		done <- outcome{res, err}
	}()

	cmd := ext.nextCommand()
	ext.sendFinal(cmd.ID, sampleMeta(),
		b64Artifact("readable.md", "# A Post\n\ntorque the sprocket to 9 Nm\n"),
		b64Artifact(HighlightsArtifact, highlights),
	)

	got := <-done
	if got.err != nil {
		t.Fatalf("capture: %v", got.err)
	}

	body := readArtifact(t, got.res.Path, HighlightsArtifact)
	var parsed struct {
		Version    int `json:"version"`
		Highlights []struct {
			ID     string `json:"id"`
			Text   string `json:"text"`
			Anchor struct {
				StartChar int    `json:"startChar"`
				EndChar   int    `json:"endChar"`
				Quote     string `json:"quote"`
			} `json:"anchor"`
			Comment string `json:"comment"`
		} `json:"highlights"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("highlights.json did not survive the envelope as JSON: %v", err)
	}
	if len(parsed.Highlights) != 1 {
		t.Fatalf("highlights = %d, want 1", len(parsed.Highlights))
	}
	h := parsed.Highlights[0]
	if h.ID != "hl-1" || h.Comment != "check this on the bench" {
		t.Errorf("highlight = %+v", h)
	}
	// The anchor is what makes a highlight resolvable back to the page:
	// offsets plus the quote, the same shape citation.ts resolves.
	if h.Anchor.StartChar != 3200 || h.Anchor.EndChar != 3226 || h.Anchor.Quote == "" {
		t.Errorf("anchor = %+v, want offsets and a quote", h.Anchor)
	}

	var listed bool
	for _, name := range got.res.Artifacts {
		if name == HighlightsArtifact {
			listed = true
		}
	}
	if !listed {
		t.Errorf("artifacts = %v, want %s listed", got.res.Artifacts, HighlightsArtifact)
	}
}
