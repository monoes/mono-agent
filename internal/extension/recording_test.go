package extension

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/recording"
)

// nextAck reads frames until the recording ack for id arrives.
func (f *fakeExtension) nextAck(id string) *Response {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = f.conn.SetReadDeadline(deadline)
		_, msg, err := f.conn.ReadMessage()
		if err != nil {
			f.t.Fatalf("read ack %s: %v", id, err)
		}
		var resp Response
		if json.Unmarshal(msg, &resp) == nil && resp.Type == RecordingAckType && resp.ID == id {
			return &resp
		}
	}
}

func recFrame(id, op string, extra map[string]any) map[string]any {
	f := map[string]any{"kind": KindRecording, "op": op, "recordingId": "rec-ws"}
	if id != "" {
		f["id"] = id
	}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

func TestRecordingFramesOverWebSocketLandAsEnvelope(t *testing.T) {
	_, ext, inbox := startCaptureServer(t)

	ext.send(recFrame("a1", "start", map[string]any{"url": "https://example.com/x", "title": "X", "goal": "g", "tabId": 3}))
	if ack := ext.nextAck("a1"); !ack.Success {
		t.Fatalf("start refused: %s", ack.Error)
	}
	ext.send(recFrame("a2", "event", map[string]any{"event": map[string]any{
		"id": "e1", "seq": 1, "type": "type", "url": "https://example.com/x", "value": "s3cret",
		"target": map[string]any{"tag": "input", "inputType": "password", "candidates": []any{}}}}))
	if ack := ext.nextAck("a2"); !ack.Success {
		t.Fatalf("event refused: %s", ack.Error)
	}
	// No id: applied, not acked.
	ext.send(recFrame("", "snapshot", map[string]any{"eventId": "e1", "name": "dom-e1.html", "data": "<input>"}))
	ext.send(recFrame("a3", "snapshot", map[string]any{"eventId": "e1", "name": "../../evil", "data": "x"}))
	if ack := ext.nextAck("a3"); ack.Success {
		t.Fatal("traversal snapshot name accepted")
	}
	ext.send(recFrame("a4", "stop", map[string]any{"reason": "user"}))
	ack := ext.nextAck("a4")
	if !ack.Success {
		t.Fatalf("stop failed: %s", ack.Error)
	}
	data, _ := ack.Data.(map[string]any)
	path, _ := data["path"].(string)
	if filepath.Dir(path) != inbox || data["recordingId"] != "rec-ws" {
		t.Fatalf("stop ack data = %v", data)
	}
	meta, err := capture.ReadMeta(path)
	if err != nil || meta.Source != recording.SourceRecording || meta.URL != "https://example.com/x" {
		t.Fatalf("meta = %+v, %v", meta, err)
	}
	raw, _ := os.ReadFile(filepath.Join(path, recording.EventsArtifact))
	if strings.Contains(string(raw), "s3cret") || !strings.Contains(string(raw), `"masked":true`) {
		t.Fatalf("events.jsonl = %s", raw)
	}
	if _, err := os.Stat(filepath.Join(path, "dom-e1.html")); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
}

func TestRecordingIdleReaperRunsWithoutAConnection(t *testing.T) {
	prev := recordingReapInterval
	recordingReapInterval = 20 * time.Millisecond
	t.Cleanup(func() { recordingReapInterval = prev })
	srv, ext, inbox := startCaptureServer(t)

	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	srv.SetRecordingIngest(&recording.Ingest{Writer: &capture.Writer{Inbox: inbox}, Now: clock})
	done := make(chan string, 1)
	srv.OnRecording(func(res *capture.Result, err error) {
		if err == nil {
			done <- res.Path
		}
	})
	ext.send(recFrame("b1", "start", map[string]any{"url": "https://example.com/"}))
	ext.nextAck("b1")
	_ = ext.conn.Close() // the service worker goes away mid-recording

	mu.Lock()
	now = now.Add(recording.DefaultIdleTimeout + time.Minute)
	mu.Unlock()
	select {
	case path := <-done:
		meta, _ := capture.ReadMeta(path)
		var reason string
		_ = json.Unmarshal(meta.Extra[recording.ExtraStopReason], &reason)
		if reason != recording.StopIdle {
			t.Fatalf("stopReason = %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle recording never reaped")
	}
}

// argvRunner records the argv it was asked to run and answers with out.
type argvRunner struct {
	mu   sync.Mutex
	args []string
	out  string
}

func (r *argvRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.args = args
	return []byte(r.out), nil
}

func TestRecordMethodsExecTheCLI(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	run := &argvRunner{out: `{"recordings":[]}`}
	srv.SetRecordRunner(run)

	ext.ask("q1", MethodRecordList, map[string]any{"profile": "work"})
	reply := ext.settled()
	if !reply.OK {
		t.Fatalf("record.list: %s", reply.Error)
	}
	if got := strings.Join(run.args, " "); got != "--profile=work record list --json" {
		t.Fatalf("argv = %q", got)
	}

	ext.ask("q2", MethodRecordAnalyze, map[string]any{"recordingId": "--help"})
	if reply := ext.settled(); reply.OK || reply.Code != CodeBadParams {
		t.Fatalf("injection accepted: %+v", reply)
	}
}

func TestRecordMethodArgsValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	drafts, _ := recording.DraftsDir()
	draft := filepath.Join(drafts, "rec-1")
	if err := os.MkdirAll(draft, 0o700); err != nil {
		t.Fatal(err)
	}
	realDraft, _ := filepath.EvalSymlinks(draft)
	req := func(p map[string]any) *Request { return &Request{Params: p} }

	good := []struct {
		build func(*Request) ([]string, error)
		p     map[string]any
		want  string
	}{
		{recordAnalyzeArgs, map[string]any{"recordingId": "2026-09-25T09-00-00Z-x", "automation": "acme"},
			"record analyze 2026-09-25T09-00-00Z-x --automation=acme --json"},
		{recordVerifyArgs, map[string]any{"draftDir": draft, "full": true}, "record verify " + realDraft + " --full --json"},
		{recordVerifyArgs, map[string]any{"draftDir": "rec-1"}, "record verify " + realDraft + " --json"},
		{recordSaveArgs, map[string]any{"draftDir": draft, "saveAs": "fragment", "new": "my-site", "name": "Create contact"},
			"record save " + realDraft + " --as=fragment --new=my-site --name=Create contact --json"},
	}
	for _, g := range good {
		args, err := g.build(req(g.p))
		if err != nil || strings.Join(args, " ") != g.want {
			t.Errorf("%v → %q, %v; want %q", g.p, strings.Join(args, " "), err, g.want)
		}
	}

	bad := []struct {
		build func(*Request) ([]string, error)
		p     map[string]any
	}{
		{recordAnalyzeArgs, map[string]any{}},
		{recordAnalyzeArgs, map[string]any{"recordingId": "../x"}},
		{recordAnalyzeArgs, map[string]any{"recordingId": "-rf"}},
		{recordAnalyzeArgs, map[string]any{"recordingId": "ok", "automation": "--exec=x"}},
		{recordAnalyzeArgs, map[string]any{"recordingId": "ok", "profile": "../../x"}},
		{recordListArgs, map[string]any{"profile": "-x"}},
		{recordVerifyArgs, map[string]any{"draftDir": "/etc"}},
		{recordVerifyArgs, map[string]any{"draftDir": drafts + "/../"}},
		{recordVerifyArgs, map[string]any{"draftDir": "--full"}},
		{recordVerifyArgs, map[string]any{"draftDir": filepath.Join(drafts, "missing")}},
		{recordSaveArgs, map[string]any{"draftDir": draft, "saveAs": "shell"}},
		{recordSaveArgs, map[string]any{"draftDir": draft, "automation": "a1", "new": "b2"}},
		{recordSaveArgs, map[string]any{"draftDir": draft, "new": "UPPER"}},
		{recordSaveArgs, map[string]any{"draftDir": draft, "name": "-x"}},
		{recordSaveArgs, map[string]any{"draftDir": draft, "name": "a\nb"}},
	}
	for _, b := range bad {
		if args, err := b.build(req(b.p)); err == nil {
			t.Errorf("%v accepted: %v", b.p, args)
		} else {
			var re *RequestError
			if !asRequestError(err, &re) || re.Code != CodeBadParams {
				t.Errorf("%v: error %v has no bad_params code", b.p, err)
			}
		}
	}
}

func TestRecordMethodDeadlines(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	if got := srv.requestDeadline(MethodRecordAnalyze); got != recordAnalyzeTimeout {
		t.Fatalf("analyze deadline = %s", got)
	}
	if got := srv.requestDeadline(MethodDocLookup); got != requestTimeout {
		t.Fatalf("default deadline = %s", got)
	}
}

func TestSelfRunnerRefusesForeignBinary(t *testing.T) {
	_, err := selfRunner{}.Run(context.Background(), "record", "list", "--json")
	var re *RequestError
	if err == nil || !asRequestError(err, &re) || re.Code != CodeUnavailable {
		t.Fatalf("test binary exec'd as monoagentcli: %v", err)
	}
}
