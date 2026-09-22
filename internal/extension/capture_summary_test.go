package extension

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/capturesummary"
)

// A summary never holds up the capture: the envelope is written and
// reported first, and summary.md turns up beside it afterwards.
func TestSummaryIsWrittenAfterTheCaptureLands(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	release := make(chan struct{})
	sum := capturesummary.New("stubtime", func(ctx context.Context, runtime, prompt string) (capturesummary.Answer, error) {
		<-release
		return capturesummary.Answer{Text: "## TL;DR\nA post about posts."}, nil
	})
	finished := make(chan capturesummary.Status, 1)
	sum.OnDone = func(dir string, st capturesummary.Status) { finished <- st }
	t.Cleanup(sum.Close)
	srv.SetAfterWrite(sum.Handle)

	landed := make(chan *capture.Result, 1)
	srv.OnCapture(func(res *capture.Result, err error) {
		if err != nil {
			t.Errorf("capture failed: %v", err)
			return
		}
		landed <- res
	})

	meta := sampleMeta()
	meta["summarize"] = map[string]any{"kind": "page"}
	meta["captureMode"] = "summary"
	ext.sendFinal("ext-summary-1", meta, b64Artifact(capture.ArtifactReadable, "# A Post\n\nPosts are good."))

	var res *capture.Result
	select {
	case res = <-landed:
	case <-time.After(5 * time.Second):
		t.Fatal("the capture never landed while its summary was still running")
	}
	st, err := capturesummary.ReadStatus(res.Path)
	if err != nil || (st.Status != capturesummary.StatePending && st.Status != capturesummary.StateRunning) {
		t.Fatalf("while running, summary.json = %+v, %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(res.Path, capturesummary.SummaryFile)); err == nil {
		t.Fatal("summary.md existed before the runtime answered")
	}

	close(release)
	select {
	case st := <-finished:
		if st.Status != capturesummary.StateDone || st.Runtime != "stubtime" {
			t.Fatalf("finished status = %+v", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("summary never finished")
	}
	doc := readArtifact(t, res.Path, capturesummary.SummaryFile)
	if !strings.Contains(doc, "A post about posts.") || !strings.Contains(doc, "# Summary: A Post") {
		t.Fatalf("summary.md = %q", doc)
	}
}

// The hook runs for a requested capture too, not only a flushed one.
func TestAfterWriteRunsForRequestedCapture(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	seen := make(chan string, 1)
	srv.SetAfterWrite(func(res *capture.Result) { seen <- res.Path })

	done := make(chan error, 1)
	go func() {
		_, err := srv.CapturePage(CaptureRequest{Mode: "screenshot", Timeout: 5 * time.Second})
		done <- err
	}()
	cmd := ext.nextCommand()
	if cmd.Params["mode"] != "screenshot" {
		t.Fatalf("params.mode = %v", cmd.Params["mode"])
	}
	if _, has := cmd.Params["formats"]; has {
		t.Fatalf("a mode with no explicit formats must let the extension choose them, got %v", cmd.Params["formats"])
	}
	ext.sendFinal(cmd.ID, sampleMeta(), b64Artifact(capture.ArtifactScreenshot, "png"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("after-write hook never ran for a requested capture")
	}
}

func TestCaptureModeSurvivesTheRelay(t *testing.T) {
	cmd := CaptureRequest{Mode: "video"}.command()
	got := captureRequestFromCommand(cmd, time.Second, "")
	if got.Mode != "video" || len(got.Formats) != 0 {
		t.Fatalf("relayed request = %+v", got)
	}
	explicit := CaptureRequest{Mode: "summary", Formats: []string{"readable"}}.command()
	if f := stringSlice(explicit.Params["formats"]); len(f) != 1 || f[0] != "readable" {
		t.Fatalf("explicit formats lost: %v", explicit.Params["formats"])
	}
}

// A hook that panics must not take the bridge down with it.
func TestAfterWritePanicIsContained(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	srv.SetAfterWrite(func(*capture.Result) { panic("boom") })
	landed := make(chan *capture.Result, 1)
	srv.OnCapture(func(res *capture.Result, err error) { landed <- res })
	ext.sendFinal("ext-panic-1", sampleMeta(), b64Artifact(capture.ArtifactReadable, "# A Post"))
	select {
	case res := <-landed:
		if res == nil {
			t.Fatal("capture was lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture never reported")
	}
}
