package capturesummary

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// A capture that names its own runtime and model (the extension's "AI for
// summaries" picker) is summarized by exactly that, once the scan has found
// the runtime, and summary.json and summary.md both say so.

const pickedMeta = `{"url":"https://paper.test/lighthouse","title":"The Lighthouse","capturedAt":"2026-09-22T12:00:00Z",
 "summarize":{"kind":"page","runtime":"opencode","model":"claude-haiku-4-5-20251001"}}`

func pickedEnvelope(t *testing.T, meta string) (*capture.Result, *stub, *Summarizer) {
	t.Helper()
	res := writeEnvelope(t, meta, map[string]string{"readable.md": "# The Lighthouse\n\nThe keeper kept a ledger."})
	s := &stub{answer: Answer{Text: "## TL;DR\nA ledger."}}
	sum := New("claude", s.run)
	sum.Catalog = (&fakeCatalog{now: time.Unix(1000, 0)}).catalog()
	return res, s, sum
}

func TestRequestOfReadsTheChoice(t *testing.T) {
	res := writeEnvelope(t, pickedMeta, map[string]string{"readable.md": "x"})
	req, ok := RequestOf(res.Meta)
	if !ok || req.Kind != KindPage || req.Runtime != "opencode" || req.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("RequestOf = %+v, %v", req, ok)
	}
}

func TestCaptureChoiceIsUsedAndRecorded(t *testing.T) {
	res, s, sum := pickedEnvelope(t, pickedMeta)
	st := sum.Summarize(context.Background(), res.Path, res.Meta, KindPage)
	if st.Status != StateDone || st.Runtime != "opencode" || st.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("status = %+v", st)
	}
	if !strings.HasPrefix(s.prompts[0], "opencode (claude-haiku-4-5-20251001)\n") {
		t.Errorf("runner got %q, want the picked runtime and model", strings.SplitN(s.prompts[0], "\n", 2)[0])
	}
	onDisk, err := ReadStatus(res.Path)
	if err != nil || onDisk.Runtime != "opencode" || onDisk.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("summary.json = %+v, %v", onDisk, err)
	}
	doc, _ := os.ReadFile(filepath.Join(res.Path, SummaryFile))
	if !strings.Contains(string(doc), "written by opencode (claude-haiku-4-5-20251001) on ") {
		t.Errorf("summary.md footer does not name the model:\n%s", doc)
	}
}

// No choice: the bridge's default runtime, and no --model at all.
func TestNoChoiceUsesTheDefault(t *testing.T) {
	res, s, sum := pickedEnvelope(t, pageMeta)
	st := sum.Summarize(context.Background(), res.Path, res.Meta, KindPage)
	if st.Status != StateDone || st.Runtime != "claude" || st.Model != "" {
		t.Fatalf("status = %+v", st)
	}
	if !strings.HasPrefix(s.prompts[0], "claude\n") {
		t.Errorf("runner got %q", strings.SplitN(s.prompts[0], "\n", 2)[0])
	}
	blob, _ := os.ReadFile(filepath.Join(res.Path, StatusFile))
	if strings.Contains(string(blob), `"model"`) {
		t.Errorf("summary.json names a model nobody picked:\n%s", blob)
	}
}

// A model alone rides on the default runtime without a scan.
func TestModelOnlyChoiceKeepsTheDefaultRuntime(t *testing.T) {
	meta := `{"url":"https://p.test/","title":"P","capturedAt":"2026-09-22T12:00:00Z","summarize":{"kind":"page","model":"claude-sonnet-5"}}`
	res, s, sum := pickedEnvelope(t, meta)
	sum.Catalog = nil // proves no lookup was needed
	st := sum.Summarize(context.Background(), res.Path, res.Meta, KindPage)
	if st.Status != StateDone || st.Runtime != "claude" || st.Model != "claude-sonnet-5" {
		t.Fatalf("status = %+v", st)
	}
	if !strings.HasPrefix(s.prompts[0], "claude (claude-sonnet-5)\n") {
		t.Errorf("runner got %q", strings.SplitN(s.prompts[0], "\n", 2)[0])
	}
}

// Whatever a browser sends, only an installed runtime id and a model-shaped
// string ever reach the runner.
func TestUnacceptableChoicesAreRefusedAndRecorded(t *testing.T) {
	cases := map[string]string{
		`{"kind":"page","runtime":"codex"}`:                   "codex is not installed",
		`{"kind":"page","runtime":"/bin/sh"}`:                 "not an agent runtime id",
		`{"kind":"page","runtime":"claude","model":"--yolo"}`: "not a model id",
		`{"kind":"page","model":"a b"}`:                       "not a model id",
	}
	for summarize, want := range cases {
		meta := `{"url":"https://p.test/","title":"P","capturedAt":"2026-09-22T12:00:00Z","summarize":` + summarize + `}`
		res, s, sum := pickedEnvelope(t, meta)
		st := sum.Summarize(context.Background(), res.Path, res.Meta, KindPage)
		if st.Status != StateError || !strings.Contains(st.Error, want) {
			t.Errorf("%s: status = %+v, want an error containing %q", summarize, st, want)
		}
		if len(s.prompts) != 0 {
			t.Errorf("%s: the runner was called", summarize)
		}
		if onDisk, err := ReadStatus(res.Path); err != nil || onDisk.Status != StateError {
			t.Errorf("%s: summary.json = %+v, %v", summarize, onDisk, err)
		}
	}
}

// A bridge with no catalog cannot check another runtime, so it refuses one.
func TestNoCatalogRefusesAnotherRuntime(t *testing.T) {
	res, s, sum := pickedEnvelope(t, pickedMeta)
	sum.Catalog = nil
	st := sum.Summarize(context.Background(), res.Path, res.Meta, KindPage)
	if st.Status != StateError || !strings.Contains(st.Error, "only writes summaries with claude") || len(s.prompts) != 0 {
		t.Fatalf("status = %+v, prompts = %d", st, len(s.prompts))
	}
}

// The pending record already says which AI was asked for.
func TestEnqueueRecordsTheRequestedChoice(t *testing.T) {
	res, _, sum := pickedEnvelope(t, pickedMeta)
	block := make(chan struct{})
	sum.Run = func(ctx context.Context, _ Target, _ string) (Answer, error) {
		<-block
		return Answer{Text: "x"}, nil
	}
	t.Cleanup(func() { close(block); sum.Close() })
	if err := sum.Enqueue(res.Path, res.Meta, KindPage); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(filepath.Join(res.Path, StatusFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"runtime": "opencode"`) || !strings.Contains(string(blob), `"model": "claude-haiku-4-5-20251001"`) {
		t.Errorf("pending summary.json:\n%s", blob)
	}
}
