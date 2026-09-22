package capturesummary

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// writeEnvelope lands a real envelope through capture.Writer, so the tests
// summarize exactly what the bridge would have written.
func writeEnvelope(t *testing.T, meta string, artifacts map[string]string) *capture.Result {
	t.Helper()
	var m capture.Meta
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		t.Fatal(err)
	}
	env := &capture.Envelope{Meta: m, Artifacts: map[string]capture.Artifact{}}
	for name, body := range artifacts {
		env.Artifacts[name] = capture.Inline([]byte(body))
	}
	w := &capture.Writer{Inbox: t.TempDir(), Now: func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) }}
	res, err := w.Write(env)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const pageMeta = `{"url":"https://paper.test/lighthouse","title":"The Lighthouse","capturedAt":"2026-09-22T12:00:00Z","summarize":{"kind":"page"}}`

const testVideoMeta = `{"url":"https://www.youtube.com/watch?v=jNQXAC9IVRw","title":"Me at the zoo","capturedAt":"2026-09-22T12:00:00Z",
 "summarize":{"kind":"video"},
 "video":{"videoId":"jNQXAC9IVRw","channel":"jawed","publishDate":"2005-04-23","duration":"0:19",
          "description":"The first video.","chapters":[{"start":0,"title":"Intro"},{"start":5,"title":"The cool thing"}]}}`

type stub struct {
	mu      sync.Mutex
	prompts []string
	answer  Answer
	err     error
	block   chan struct{}
	running atomic.Int32
	maxSeen atomic.Int32
}

func (s *stub) run(ctx context.Context, target Target, prompt string) (Answer, error) {
	n := s.running.Add(1)
	defer s.running.Add(-1)
	for {
		m := s.maxSeen.Load()
		if n <= m || s.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	s.mu.Lock()
	s.prompts = append(s.prompts, target.String()+"\n"+prompt)
	s.mu.Unlock()
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return Answer{}, ctx.Err()
		}
	}
	return s.answer, s.err
}

func TestRequested(t *testing.T) {
	cases := map[string]string{
		`{"url":"u"}`:                              "",
		`{"url":"u","summarize":false}`:            "",
		`{"url":"u","summarize":true}`:             KindPage,
		`{"url":"u","summarize":{"kind":"video"}}`: KindVideo,
		`{"url":"u","summarize":{"kind":"odd"}}`:   KindPage,
	}
	for raw, want := range cases {
		var m capture.Meta
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		got, ok := Requested(m)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q,%v want %q", raw, got, ok, want)
		}
	}
}

func TestSummarizePageWritesSummaryAndStatus(t *testing.T) {
	res := writeEnvelope(t, pageMeta, map[string]string{"readable.md": "# The Lighthouse\n\nThe keeper kept a ledger for forty years."})
	s := &stub{answer: Answer{Text: "## TL;DR\nA ledger.\n\n## Key points\n- forty years", CostUSD: 0.12}}
	sum := New("", s.run)
	st := sum.Summarize(context.Background(), res.Path, res.Meta, KindPage)
	if st.Status != StateDone || st.Runtime != "claude" || st.Source != "readable.md" || st.CostUSD != 0.12 {
		t.Fatalf("status = %+v", st)
	}
	doc, err := os.ReadFile(filepath.Join(res.Path, SummaryFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Summary: The Lighthouse", "Page summary of <https://paper.test/lighthouse>", "written by claude", "## TL;DR\nA ledger."} {
		if !strings.Contains(string(doc), want) {
			t.Errorf("summary.md missing %q:\n%s", want, doc)
		}
	}
	onDisk, err := ReadStatus(res.Path)
	if err != nil || onDisk.Status != StateDone {
		t.Fatalf("summary.json = %+v, %v", onDisk, err)
	}
	prompt := s.prompts[0]
	if !strings.HasPrefix(prompt, "claude\n") || !strings.Contains(prompt, "## Key points") ||
		!strings.Contains(prompt, `<content source="readable.md">`) || strings.Contains(prompt, "## Highlights") {
		t.Errorf("page prompt:\n%s", prompt)
	}
	// The envelope itself is untouched: meta.json still reads as a capture.
	if _, err := capture.ReadMeta(res.Path); err != nil {
		t.Fatal(err)
	}
}

func TestSummarizeVideoUsesTranscriptAndAsksForHighlights(t *testing.T) {
	res := writeEnvelope(t, testVideoMeta, map[string]string{
		"readable.md":   "# Me at the zoo",
		"transcript.md": "[0:01](https://www.youtube.com/watch?v=jNQXAC9IVRw&t=1s) All right, so here we are",
	})
	s := &stub{answer: Answer{Text: "## TL;DR\nElephants."}}
	st := New("codex", s.run).Summarize(context.Background(), res.Path, res.Meta, KindVideo)
	if st.Status != StateDone || st.Source != "transcript.md" || st.Runtime != "codex" {
		t.Fatalf("status = %+v", st)
	}
	p := s.prompts[0]
	for _, want := range []string{"YouTube video", "## Highlights", "Channel: jawed", "Duration: 0:19", "- 0:05 The cool thing", `<content source="transcript.md">`, "here we are"} {
		if !strings.Contains(p, want) {
			t.Errorf("video prompt missing %q:\n%s", want, p)
		}
	}
}

func TestVideoWithoutTranscriptFallsBackToDescription(t *testing.T) {
	res := writeEnvelope(t, testVideoMeta, map[string]string{"screenshot.png": "png"})
	s := &stub{answer: Answer{Text: "## TL;DR\nA video."}}
	st := New("claude", s.run).Summarize(context.Background(), res.Path, res.Meta, KindVideo)
	if st.Status != StateDone || !strings.Contains(st.Source, "description") {
		t.Fatalf("status = %+v", st)
	}
	if strings.Contains(s.prompts[0], "## Highlights") {
		t.Error("no transcript, so no timestamps to highlight")
	}
}

func TestFailuresAreRecordedNotFatal(t *testing.T) {
	t.Run("runtime error", func(t *testing.T) {
		res := writeEnvelope(t, pageMeta, map[string]string{"readable.md": "text"})
		st := New("claude", (&stub{err: errors.New("claude CLI not installed")}).run).Summarize(context.Background(), res.Path, res.Meta, KindPage)
		if st.Status != StateError || !strings.Contains(st.Error, "claude CLI not installed") {
			t.Fatalf("status = %+v", st)
		}
		if _, err := os.Stat(filepath.Join(res.Path, SummaryFile)); !errors.Is(err, os.ErrNotExist) {
			t.Error("a failed summary must not leave a summary.md")
		}
		if disk, _ := ReadStatus(res.Path); disk == nil || disk.Status != StateError {
			t.Errorf("summary.json = %+v", disk)
		}
	})
	t.Run("empty answer", func(t *testing.T) {
		res := writeEnvelope(t, pageMeta, map[string]string{"readable.md": "text"})
		st := New("claude", (&stub{answer: Answer{Text: "  "}}).run).Summarize(context.Background(), res.Path, res.Meta, KindPage)
		if st.Status != StateError || !strings.Contains(st.Error, "empty") {
			t.Fatalf("status = %+v", st)
		}
	})
	t.Run("nothing to summarize", func(t *testing.T) {
		res := writeEnvelope(t, pageMeta, map[string]string{"screenshot.png": "png"})
		st := New("claude", (&stub{}).run).Summarize(context.Background(), res.Path, res.Meta, KindPage)
		if st.Status != StateError || !strings.Contains(st.Error, "nothing to summarize") {
			t.Fatalf("status = %+v", st)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		res := writeEnvelope(t, pageMeta, map[string]string{"readable.md": "text"})
		sum := New("claude", (&stub{block: make(chan struct{})}).run)
		sum.Timeout = 20 * time.Millisecond
		st := sum.Summarize(context.Background(), res.Path, res.Meta, KindPage)
		if st.Status != StateError || !strings.Contains(st.Error, "did not answer within") {
			t.Fatalf("status = %+v", st)
		}
	})
}

func TestLongInputIsClipped(t *testing.T) {
	long := strings.Repeat("a", 500) + "MIDDLE" + strings.Repeat("z", 500)
	res := writeEnvelope(t, pageMeta, map[string]string{"readable.md": long})
	in, err := LoadInput(res.Path, res.Meta, KindPage, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !in.Truncated || strings.Contains(in.Content, "MIDDLE") || !strings.HasPrefix(in.Content, strings.Repeat("a", 80)) || !strings.HasSuffix(in.Content, strings.Repeat("z", 20)) {
		t.Fatalf("clipped = %q", in.Content)
	}
}

// Handle is what the bridge calls after every write: only captures that
// asked are summarized, they run one at a time, and each is marked pending
// the moment it is queued.
func TestHandleQueuesOneAtATime(t *testing.T) {
	s := &stub{answer: Answer{Text: "## TL;DR\nok"}, block: make(chan struct{})}
	sum := New("claude", s.run)
	var mu sync.Mutex
	done := map[string]Status{}
	sum.OnDone = func(dir string, st Status) { mu.Lock(); done[dir] = st; mu.Unlock() }
	defer sum.Close()

	plain := writeEnvelope(t, `{"url":"https://paper.test/plain","title":"Plain"}`, map[string]string{"readable.md": "x"})
	sum.Handle(plain)
	if _, err := ReadStatus(plain.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a capture that asked for nothing got a summary status")
	}

	var dirs []string
	for i := 0; i < 3; i++ {
		res := writeEnvelope(t, pageMeta, map[string]string{"readable.md": "text"})
		sum.Handle(res)
		dirs = append(dirs, res.Path)
		if st, err := ReadStatus(res.Path); err != nil || (st.Status != StatePending && st.Status != StateRunning) {
			t.Fatalf("queued capture status = %+v, %v", st, err)
		}
	}
	close(s.block)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(done)
		mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of 3 summaries finished", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := s.maxSeen.Load(); got != 1 {
		t.Errorf("%d summaries ran at once; want 1", got)
	}
	for _, d := range dirs {
		if done[d].Status != StateDone {
			t.Errorf("%s: %+v", d, done[d])
		}
	}
}

func TestQueueOverflowIsRecorded(t *testing.T) {
	s := &stub{answer: Answer{Text: "ok"}, block: make(chan struct{})}
	sum := New("claude", s.run)
	defer func() { close(s.block); sum.Close() }()
	var last *capture.Result
	for i := 0; i < queueDepth+3; i++ {
		last = writeEnvelope(t, pageMeta, map[string]string{"readable.md": "text"})
		sum.Handle(last)
	}
	st, err := ReadStatus(last.Path)
	if err != nil || st.Status != StateError || !strings.Contains(st.Error, "too many summaries") {
		t.Fatalf("overflowed capture status = %+v, %v", st, err)
	}
}

func TestDisabledRuntimeSaysSo(t *testing.T) {
	res := writeEnvelope(t, pageMeta, map[string]string{"readable.md": "text"})
	sum := New("off", (&stub{}).run)
	sum.Handle(res)
	st, err := ReadStatus(res.Path)
	if err != nil || st.Status != StateError || !strings.Contains(st.Error, "turned off") {
		t.Fatalf("status = %+v, %v", st, err)
	}
}

func TestStalled(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	old := &Status{Status: StatePending, RequestedAt: stamp(now.Add(-time.Hour))}
	fresh := &Status{Status: StateRunning, StartedAt: stamp(now.Add(-time.Minute))}
	finished := &Status{Status: StateDone, RequestedAt: stamp(now.Add(-time.Hour))}
	if !old.Stalled(now) || fresh.Stalled(now) || finished.Stalled(now) {
		t.Errorf("stalled: old=%v fresh=%v done=%v", old.Stalled(now), fresh.Stalled(now), finished.Stalled(now))
	}
}
