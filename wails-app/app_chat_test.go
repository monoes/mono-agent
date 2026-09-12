package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/ai"
	aichat "github.com/monoes/mono-agent/internal/ai/chat"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/storage"
)

// --- test helpers ---

// newChatTestStore returns both the AIStore and the raw *sql.DB behind it —
// ChatService's constructor needs the raw handle directly (CanvasTools
// queries tables AIStore doesn't own), so callers must keep both rather
// than trying to recover one from the other after the fact.
func newChatTestStore(t *testing.T) (*ai.AIStore, *sql.DB) {
	t.Helper()
	keyring.MockInit()
	sdb, err := storage.NewDatabase(filepath.Join(t.TempDir(), "chat-sup-test.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := sdb.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { sdb.DB.Close() })
	store, err := ai.NewAIStore(sdb.DB)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}
	return store, sdb.DB
}

// jsonUnmarshalPayload decodes one event's payload into target.
func jsonUnmarshalPayload(ev chatevents.Event, target any) error {
	return json.Unmarshal(ev.Payload, target)
}

// collectingEmitter records every emitted event, safe for concurrent use
// (the supervisor's writer goroutine is the only emitter for a given turn,
// but a test may hold its own goroutine reading this concurrently).
type collectingEmitter struct {
	mu     sync.Mutex
	events []chatevents.Event
}

func (c *collectingEmitter) emit(ev chatevents.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *collectingEmitter) snapshot() []chatevents.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]chatevents.Event, len(c.events))
	copy(out, c.events)
	return out
}

func (c *collectingEmitter) byType(typ chatevents.EventType) []chatevents.Event {
	var out []chatevents.Event
	for _, ev := range c.snapshot() {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// waitForType polls (test-locally, no production sleep) until at least one
// event of typ has been emitted, or fails the test after timeout.
func waitForType(t *testing.T, c *collectingEmitter, typ chatevents.EventType, timeout time.Duration) []chatevents.Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := c.byType(typ); len(got) > 0 {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a %s event; got: %+v", typ, c.snapshot())
	return nil
}

// fakeChatProcess is an injectable chatProcess for driving chatSupervisor
// without a real monoagentcli binary or real subprocess. Stdout is backed
// by an io.Pipe so a test can control exactly what arrives and when EOF
// happens, mirroring how a real process's pipe closes when it exits or is
// killed.
type fakeChatProcess struct {
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderr  string
	waitErr error

	// stderrDelay, when nonzero, delays the first Read from Stderr() by this
	// duration. Used to deterministically reproduce a stderr-reader
	// goroutine that hasn't been scheduled yet by the time runAgentTurn's
	// wait goroutine calls proc.Wait() and reads the shared buffer, instead
	// of depending on scheduler luck. Zero (the default) preserves the
	// exact immediate strings.Reader behavior every other test relies on.
	stderrDelay time.Duration

	mu       sync.Mutex
	killed   bool
	exited   chan struct{}
	exitOnce sync.Once
}

func newFakeChatProcess(stderr string) *fakeChatProcess {
	r, w := io.Pipe()
	return &fakeChatProcess{stdoutR: r, stdoutW: w, stderr: stderr, exited: make(chan struct{})}
}

func (p *fakeChatProcess) Stdout() io.Reader { return p.stdoutR }

func (p *fakeChatProcess) Stderr() io.Reader {
	var r io.Reader = strings.NewReader(p.stderr)
	if p.stderrDelay > 0 {
		r = &delayedReader{r: r, delay: p.stderrDelay}
	}
	return r
}

// delayedReader sleeps once, on its first Read, before delegating to the
// wrapped reader. See fakeChatProcess.stderrDelay.
type delayedReader struct {
	r     io.Reader
	delay time.Duration
	once  sync.Once
}

func (d *delayedReader) Read(p []byte) (int, error) {
	d.once.Do(func() { time.Sleep(d.delay) })
	return d.r.Read(p)
}

// writeLine pushes one NDJSON line to the stdout reader.
func (p *fakeChatProcess) writeLine(s string) {
	_, _ = p.stdoutW.Write([]byte(s + "\n"))
}

// endStream simulates the process exiting on its own (stdout EOFs, then
// Wait returns waitErr).
func (p *fakeChatProcess) endStream(waitErr error) {
	p.waitErr = waitErr
	_ = p.stdoutW.Close()
	p.exitOnce.Do(func() { close(p.exited) })
}

func (p *fakeChatProcess) Wait() error {
	<-p.exited
	return p.waitErr
}

func (p *fakeChatProcess) Kill() {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	// A real killed process's stdout pipe closes as a side effect of the
	// process dying — reproduced here so the reader goroutine's scanner
	// reaches EOF and the turn can finalize.
	_ = p.stdoutW.CloseWithError(io.EOF)
	p.exitOnce.Do(func() { close(p.exited) })
}

func (p *fakeChatProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// newTestSupervisor wires a chatSupervisor to a real store (so transactional
// sequencing/compare-and-set finalize are exercised for real) but a fake
// launcher/emitter, returning the supervisor, the emitter to assert against,
// and a function to enqueue the next launched fake process.
func newTestSupervisor(t *testing.T) (*chatSupervisor, *collectingEmitter, *[]*fakeChatProcess) {
	t.Helper()
	store, db := newChatTestStore(t)
	chatSvc := aichat.NewChatService(store, db)
	emitter := &collectingEmitter{}
	var launched []*fakeChatProcess
	launcher := func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		if len(launched) == 0 {
			t.Fatal("launcher called with no queued fake process")
		}
		p := launched[0]
		launched = launched[1:]
		return p, nil
	}
	findCLI := func() (string, error) { return "fake-monoagentcli", nil }
	sup := newChatSupervisor(store, chatSvc, launcher, emitter.emit, findCLI)
	return sup, emitter, &launched
}

// --- admission tests ---

func TestChatSupervisor_DuplicateStartReturnsExistingAdmission(t *testing.T) {
	sup, _, _ := newTestSupervisor(t)
	h1, already1, err := sup.admit("conv-1", "turn-1")
	if err != nil || already1 {
		t.Fatalf("first admit: h=%v already=%v err=%v", h1, already1, err)
	}
	h2, already2, err := sup.admit("conv-1", "turn-1")
	if err != nil {
		t.Fatalf("second admit (duplicate): %v", err)
	}
	if !already2 {
		t.Error("duplicate admit with the same turnID did not report alreadyActive")
	}
	if h1 != h2 {
		t.Error("duplicate admit returned a different handle instead of the existing one")
	}
}

func TestChatSupervisor_BusyWhenDifferentTurnActive(t *testing.T) {
	sup, _, _ := newTestSupervisor(t)
	if _, already, err := sup.admit("conv-1", "turn-1"); err != nil || already {
		t.Fatalf("first admit: already=%v err=%v", already, err)
	}
	_, _, err := sup.admit("conv-1", "turn-2")
	if !errors.Is(err, errChatBusy) {
		t.Errorf("admit(conv-1, turn-2) while turn-1 active = %v, want errChatBusy", err)
	}
}

func TestChatSupervisor_ReleaseFreesAdmissionSlot(t *testing.T) {
	sup, _, _ := newTestSupervisor(t)
	sup.admit("conv-1", "turn-1")
	sup.release("conv-1", "turn-1")
	_, already, err := sup.admit("conv-1", "turn-2")
	if err != nil || already {
		t.Fatalf("admit after release: already=%v err=%v, want a clean admission", already, err)
	}
}

// --- end-to-end agent-turn tests ---

func TestChatSupervisor_NormalCompletion_EmitsInOrderAndFinishesCompleted(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, err := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	proc := newFakeChatProcess("")
	*launched = append(*launched, proc)

	h, _, err := sup.admit(conv.ID, "turn-1")
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	h.profileID = "default"
	if _, _, err := sup.store.CreateTurn(conv.ID, "default", "turn-1", sup.instanceID, "hi"); err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := sup.startAgentTurn(h, conv, "turn-1", "hi", false, false); err != nil {
		t.Fatalf("startAgentTurn: %v", err)
	}

	proc.writeLine(`{"v":1,"type":"start","runtime":"fake-runtime","cwd":"/app","pid":1}`)
	proc.writeLine(`{"v":1,"type":"session","session_id":"th_1"}`)
	proc.writeLine(`{"v":1,"type":"assistant","text":"hello"}`)
	proc.writeLine(`{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"hello"}`)
	proc.writeLine(`{"v":1,"type":"done","exit_code":0}`)
	proc.endStream(nil)

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	if err := jsonUnmarshalPayload(finished[0], &payload); err != nil {
		t.Fatalf("unmarshal turn.finished payload: %v", err)
	}
	if payload.Status != chatevents.StatusCompleted {
		t.Errorf("status = %q, want %q", payload.Status, chatevents.StatusCompleted)
	}
	if !payload.HistorySaved {
		t.Error("HistorySaved = false, want true for a normal completion")
	}

	// The turn must no longer be admitted (registry slot freed).
	if sup.lookup(conv.ID, "turn-1") != nil {
		t.Error("turn handle still registered after finalization")
	}

	// turn.started must be the first event this turn ever emitted.
	all := emitter.snapshot()
	if len(all) == 0 || all[0].Type != chatevents.EventTurnStarted {
		t.Fatalf("first event = %+v, want turn.started", all)
	}
}

func TestChatSupervisor_StopBeforeCompletion_MarksCancelledAndKillsProcess(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("")
	*launched = append(*launched, proc)

	h, _, _ := sup.admit(conv.ID, "turn-stop")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-stop", sup.instanceID, "hi")
	if err := sup.startAgentTurn(h, conv, "turn-stop", "hi", false, false); err != nil {
		t.Fatalf("startAgentTurn: %v", err)
	}

	proc.writeLine(`{"v":1,"type":"start","runtime":"fake-runtime","cwd":"/app","pid":1}`)
	// No done event ever arrives — Stop interrupts before the turn would
	// naturally finish.
	h.requestStop()

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	jsonUnmarshalPayload(finished[0], &payload)
	if payload.Status != chatevents.StatusCancelled {
		t.Errorf("status = %q, want %q", payload.Status, chatevents.StatusCancelled)
	}
	if !proc.wasKilled() {
		t.Error("requestStop() did not kill the underlying process")
	}
}

func TestChatSupervisor_StaleStopAfterCompletionIsNoOp(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("")
	*launched = append(*launched, proc)

	h, _, _ := sup.admit(conv.ID, "turn-done")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-done", sup.instanceID, "hi")
	sup.startAgentTurn(h, conv, "turn-done", "hi", false, false)

	proc.writeLine(`{"v":1,"type":"start","runtime":"fake-runtime","cwd":"/app","pid":1}`)
	proc.writeLine(`{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"ok"}`)
	proc.writeLine(`{"v":1,"type":"done","exit_code":0}`)
	proc.endStream(nil)
	waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)

	// A Stop arriving after the turn already finished must be a harmless
	// no-op: the handle is gone from the registry (StopChatTurn's binding
	// checks lookup() == nil), and calling requestStop() directly on the
	// already-finalized handle must not panic or re-finalize.
	if sup.lookup(conv.ID, "turn-done") != nil {
		t.Fatal("turn handle still registered after completion — precondition for this test is wrong")
	}
	h.requestStop() // must not panic even though the turn already finished

	turn, err := sup.store.GetTurn("turn-done", "default")
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if turn.Status != string(chatevents.StatusCompleted) {
		t.Errorf("turn status after stale Stop = %q, want it to remain %q", turn.Status, chatevents.StatusCompleted)
	}
}

func TestChatSupervisor_ForcedProcessCrashWithNoEvents_IsFailed(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("segmentation fault")
	*launched = append(*launched, proc)

	h, _, _ := sup.admit(conv.ID, "turn-crash")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-crash", sup.instanceID, "hi")
	sup.startAgentTurn(h, conv, "turn-crash", "hi", false, false)

	// The process dies before emitting a single event, AND cmd.Wait itself
	// returns an error — positive evidence of failure, not mere silence.
	proc.endStream(errors.New("exit status 139"))

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	jsonUnmarshalPayload(finished[0], &payload)
	if payload.Status != chatevents.StatusFailed {
		t.Errorf("status = %q, want %q (a process that exits before any output, with a wait error, is a failure, not silently interrupted)", payload.Status, chatevents.StatusFailed)
	}
}

// TestChatSupervisor_CleanExitWithNoDoneEvent_IsInterrupted covers
// precedence rule 3 directly at the supervisor level: the process exits
// cleanly (Wait returns nil — no positive failure evidence) but never sent
// a done event at all. Ambiguous, so interrupted, not completed and not
// failed.
func TestChatSupervisor_CleanExitWithNoDoneEvent_IsInterrupted(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("")
	*launched = append(*launched, proc)

	h, _, _ := sup.admit(conv.ID, "turn-nodone")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-nodone", sup.instanceID, "hi")
	sup.startAgentTurn(h, conv, "turn-nodone", "hi", false, false)

	proc.writeLine(`{"v":1,"type":"start","runtime":"fake-runtime","cwd":"/app","pid":1}`)
	proc.writeLine(`{"v":1,"type":"assistant","text":"partial answer, then the pipe just closed"}`)
	proc.endStream(nil) // clean exit, but no done event was ever sent

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	jsonUnmarshalPayload(finished[0], &payload)
	if payload.Status != chatevents.StatusInterrupted {
		t.Errorf("status = %q, want %q", payload.Status, chatevents.StatusInterrupted)
	}
}

func TestChatSupervisor_FatalErrorEvent_IsFailed(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("")
	*launched = append(*launched, proc)

	h, _, _ := sup.admit(conv.ID, "turn-fatal")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-fatal", sup.instanceID, "hi")
	sup.startAgentTurn(h, conv, "turn-fatal", "hi", false, false)

	proc.writeLine(`{"v":1,"type":"start","runtime":"fake-runtime","cwd":"/app","pid":1}`)
	proc.writeLine(`{"v":1,"type":"error","code":"auth","fatal":true,"message":"not logged in"}`)
	proc.writeLine(`{"v":1,"type":"done","exit_code":1}`)
	proc.endStream(nil)

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	jsonUnmarshalPayload(finished[0], &payload)
	if payload.Status != chatevents.StatusFailed {
		t.Errorf("status = %q, want %q", payload.Status, chatevents.StatusFailed)
	}
}

// TestChatSupervisor_UnknownFlagLaunchFailure is the plan's §161 case: an
// older monoagentcli that rejects --no-history must surface a clear,
// distinct failure rather than looking like a generic interrupted process.
func TestChatSupervisor_UnknownFlagLaunchFailure_ReportsDistinctNoticeThenFailed(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("Error: unknown flag: --no-history\n")
	*launched = append(*launched, proc)

	h, _, _ := sup.admit(conv.ID, "turn-oldcli")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-oldcli", sup.instanceID, "hi")
	sup.startAgentTurn(h, conv, "turn-oldcli", "hi", false, false)

	// The old CLI exits immediately, before emitting any NDJSON at all.
	proc.endStream(errors.New("exit status 1"))

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	jsonUnmarshalPayload(finished[0], &payload)
	if payload.Status != chatevents.StatusFailed {
		t.Errorf("status = %q, want %q", payload.Status, chatevents.StatusFailed)
	}

	notices := emitter.byType(chatevents.EventNotice)
	if len(notices) == 0 {
		t.Fatal("no notice event emitted for the unrecognized --no-history flag")
	}
	var noticePayload chatevents.NoticePayload
	jsonUnmarshalPayload(notices[0], &noticePayload)
	if noticePayload.Code != "cli-flag-unsupported" {
		t.Errorf("notice code = %q, want %q", noticePayload.Code, "cli-flag-unsupported")
	}
	if noticePayload.Severity != chatevents.SeverityError {
		t.Errorf("notice severity = %q, want %q", noticePayload.Severity, chatevents.SeverityError)
	}
}

// TestChatSupervisor_UnknownFlagLaunchFailure_StderrDrainedBeforeFinalize
// deterministically reproduces the pre-existing intermittent failure in the
// sibling test above (documented in
// docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md under
// "Pre-existing test flake found while bounding NoticePayload.Message"):
// runAgentTurn's wait goroutine calls proc.Wait() and then immediately
// snapshots stderrBuf, with nothing ensuring the separate stderr-draining
// goroutine has actually finished writing to it first. stderrMu only makes
// that access safe (no torn read/write); it says nothing about whether the
// buffer is complete yet. Against a real process this race is usually won
// by luck (the OS pipe already has the data buffered by the time Wait()
// returns); this test removes the luck by delaying the stderr goroutine's
// first Read, so it fails on every run against the pre-fix code instead of
// only intermittently.
func TestChatSupervisor_UnknownFlagLaunchFailure_StderrDrainedBeforeFinalize(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("Error: unknown flag: --no-history\n")
	proc.stderrDelay = 50 * time.Millisecond
	*launched = append(*launched, proc)

	h, _, _ := sup.admit(conv.ID, "turn-slow-stderr")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-slow-stderr", sup.instanceID, "hi")
	sup.startAgentTurn(h, conv, "turn-slow-stderr", "hi", false, false)

	// Same setup as the sibling test above (the old CLI exits immediately,
	// before emitting any NDJSON at all) — but here the stderr-reader
	// goroutine won't actually put anything into stderrBuf for another
	// 50ms, well after the wait goroutine's proc.Wait() has already
	// returned.
	proc.endStream(errors.New("exit status 1"))

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	jsonUnmarshalPayload(finished[0], &payload)
	if payload.Status != chatevents.StatusFailed {
		t.Errorf("status = %q, want %q", payload.Status, chatevents.StatusFailed)
	}

	notices := emitter.byType(chatevents.EventNotice)
	if len(notices) == 0 {
		t.Fatal("no notice event emitted for the unrecognized --no-history flag — stderrBuf was evidently read before the stderr-draining goroutine finished writing to it")
	}
	var noticePayload chatevents.NoticePayload
	jsonUnmarshalPayload(notices[0], &noticePayload)
	if noticePayload.Code != "cli-flag-unsupported" {
		t.Errorf("notice code = %q, want %q", noticePayload.Code, "cli-flag-unsupported")
	}
	if noticePayload.Severity != chatevents.SeverityError {
		t.Errorf("notice severity = %q, want %q", noticePayload.Severity, chatevents.SeverityError)
	}
}

// TestChatSupervisor_NonFatalErrorNoticeIsBounded guards against an
// external adapter's error text writing an unbounded row/event payload.
// Tool results are already bounded via BoundText (app_chat.go's
// EventToolResult case); a non-fatal error's message was passed straight
// through with no cap at all.
func TestChatSupervisor_NonFatalErrorNoticeIsBounded(t *testing.T) {
	sup, emitter, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("")
	*launched = append(*launched, proc)

	h, _, _ := sup.admit(conv.ID, "turn-oversized-notice")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-oversized-notice", sup.instanceID, "hi")
	sup.startAgentTurn(h, conv, "turn-oversized-notice", "hi", false, false)

	oversized := strings.Repeat("x", chatevents.MaxToolPreviewBytes+1000)
	line, err := json.Marshal(map[string]interface{}{
		"v": 1, "type": "error", "code": "adapter-error", "fatal": false, "message": oversized,
	})
	if err != nil {
		t.Fatalf("marshal test NDJSON line: %v", err)
	}
	proc.writeLine(string(line))
	proc.writeLine(`{"v":1,"type":"done","exit_code":0}`)
	proc.endStream(nil)

	notices := waitForType(t, emitter, chatevents.EventNotice, 2*time.Second)
	var noticePayload chatevents.NoticePayload
	if err := jsonUnmarshalPayload(notices[0], &noticePayload); err != nil {
		t.Fatalf("unmarshal notice payload: %v", err)
	}
	if len(noticePayload.Message) > chatevents.MaxToolPreviewBytes {
		t.Errorf("notice message is %d bytes, want <= %d (chatevents.MaxToolPreviewBytes) — an external adapter's error text must not write an unbounded event payload", len(noticePayload.Message), chatevents.MaxToolPreviewBytes)
	}
}

// TestChatSupervisor_CommitBeforeEmit verifies the write-before-emit
// ordering directly: at the moment each event is delivered to the emitter,
// it must already be readable back from the durable store (its seq must be
// <= the turn's currently-committed high-water mark at read time), not
// merely "eventually" persisted after the fact.
func TestChatSupervisor_CommitBeforeEmit(t *testing.T) {
	sup, _, launched := newTestSupervisor(t)
	conv, _ := sup.store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	proc := newFakeChatProcess("")
	*launched = append(*launched, proc)

	var verifyErr error
	var mu sync.Mutex
	sup.emit = func(ev chatevents.Event) {
		got, err := sup.store.GetEvents(conv.ID, "turn-order", "default", ev.Seq-1, 1)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			verifyErr = err
			return
		}
		if len(got) != 1 || got[0].Seq != ev.Seq {
			verifyErr = fmt.Errorf("event seq %d not yet readable from the store at emit time (got %+v)", ev.Seq, got)
		}
	}

	h, _, _ := sup.admit(conv.ID, "turn-order")
	h.profileID = "default"
	sup.store.CreateTurn(conv.ID, "default", "turn-order", sup.instanceID, "hi")
	sup.startAgentTurn(h, conv, "turn-order", "hi", false, false)

	proc.writeLine(`{"v":1,"type":"start","runtime":"fake-runtime","cwd":"/app","pid":1}`)
	proc.writeLine(`{"v":1,"type":"session","session_id":"th_order"}`)
	proc.writeLine(`{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"ok"}`)
	proc.writeLine(`{"v":1,"type":"done","exit_code":0}`)
	proc.endStream(nil)

	deadline := time.Now().Add(2 * time.Second)
	for sup.lookup(conv.ID, "turn-order") != nil && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if verifyErr != nil {
		t.Error(verifyErr)
	}
}

// --- turn.started persistence-failure tests ---
//
// If the very first append for a turn (turn.started) fails, the turn row
// CreateTurn already wrote is left "active" in the store with nothing to
// ever finalize it — a restart's reconcileOrphanedTurns is the only thing
// that would ever clear it, and DeleteConversation refuses a conversation
// with an active turn in the meantime. Both startAgentTurn and
// startProviderTurn must finalize the turn as failed on this path instead
// of just returning the error. Simulated here by closing the store's DB
// after CreateTurn succeeds but before the turn.started append — a stand-in
// for any real mid-turn persistence failure (disk full, DB locked, etc.),
// deterministic without racing the sequence-allocation transaction itself.

func TestChatSupervisor_AgentTurnStartedPersistFailure_FinalizesFailedAndReleases(t *testing.T) {
	store, db := newChatTestStore(t)
	chatSvc := aichat.NewChatService(store, db)
	emitter := &collectingEmitter{}
	var launched []*fakeChatProcess
	launcher := func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		if len(launched) == 0 {
			t.Fatal("launcher called with no queued fake process")
		}
		p := launched[0]
		launched = launched[1:]
		return p, nil
	}
	findCLI := func() (string, error) { return "fake-monoagentcli", nil }
	sup := newChatSupervisor(store, chatSvc, launcher, emitter.emit, findCLI)

	conv, err := store.CreateConversation("default", "agent", "general", "fake-runtime", "", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	proc := newFakeChatProcess("")
	launched = append(launched, proc)

	h, _, err := sup.admit(conv.ID, "turn-1")
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	h.profileID = "default"
	if _, _, err := store.CreateTurn(conv.ID, "default", "turn-1", sup.instanceID, "hi"); err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}

	db.Close()

	if err := sup.startAgentTurn(h, conv, "turn-1", "hi", false, false); err == nil {
		t.Fatal("startAgentTurn with a dead store: want error, got nil")
	}

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	if err := jsonUnmarshalPayload(finished[0], &payload); err != nil {
		t.Fatalf("unmarshal turn.finished payload: %v", err)
	}
	if payload.Status != chatevents.StatusFailed {
		t.Errorf("status = %q, want %q", payload.Status, chatevents.StatusFailed)
	}
	if payload.HistorySaved {
		t.Error("HistorySaved = true, want false — the store write itself is what failed")
	}
	if finished[0].Seq != chatevents.MaxSafeSeq {
		// This live-only event was never allocated a real seq (the store
		// write is exactly what failed) — anything below the frontend's
		// already-applied high-water mark (e.g. the 0 this used to be) is
		// unconditionally dropped by chatReducer.js's "ev.seq <=
		// state.lastSeq" dedup guard, silently soft-locking the panel.
		t.Errorf("live-only turn.finished Seq = %d, want chatevents.MaxSafeSeq so the frontend never discards it as stale", finished[0].Seq)
	}
	if h2 := sup.lookup(conv.ID, "turn-1"); h2 != nil {
		t.Error("turn admission was not released after the turn.started append failed")
	}
}

func TestChatSupervisor_ProviderTurnStartedPersistFailure_FinalizesFailedAndReleases(t *testing.T) {
	store, db := newChatTestStore(t)
	chatSvc := aichat.NewChatService(store, db)
	emitter := &collectingEmitter{}
	launcher := func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		t.Fatal("launcher should not be called for a provider-backend turn")
		return nil, nil
	}
	findCLI := func() (string, error) { return "fake-monoagentcli", nil }
	sup := newChatSupervisor(store, chatSvc, launcher, emitter.emit, findCLI)

	conv, err := store.CreateConversation("default", "provider", "general", "", "test-provider", "test-model")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	h, _, err := sup.admit(conv.ID, "turn-1")
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	h.profileID = "default"
	if _, _, err := store.CreateTurn(conv.ID, "default", "turn-1", sup.instanceID, "hi"); err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}

	db.Close()

	if err := sup.startProviderTurn(h, conv, "hi"); err == nil {
		t.Fatal("startProviderTurn with a dead store: want error, got nil")
	}

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	if err := jsonUnmarshalPayload(finished[0], &payload); err != nil {
		t.Fatalf("unmarshal turn.finished payload: %v", err)
	}
	if payload.Status != chatevents.StatusFailed {
		t.Errorf("status = %q, want %q", payload.Status, chatevents.StatusFailed)
	}
	if finished[0].Seq != chatevents.MaxSafeSeq {
		t.Errorf("live-only turn.finished Seq = %d, want chatevents.MaxSafeSeq so the frontend never discards it as stale", finished[0].Seq)
	}
	if h2 := sup.lookup(conv.ID, "turn-1"); h2 != nil {
		t.Error("turn admission was not released after the turn.started append failed")
	}
}

// --- App binding turnId-correlation tests ---
//
// StartChatTurn's plan contract (§189) is {ok,turnId,status} on every
// branch, so a frontend keying pending requests by turnId can correlate
// each response back to the call that produced it. The busy and
// already-active branches were built by hand-writing/short-circuiting the
// JSON and dropped the field.

func newChatBindingTestApp(t *testing.T) (*App, *[]*fakeChatProcess) {
	t.Helper()
	store, db := newChatTestStore(t)
	chatSvc := aichat.NewChatService(store, db)
	var launched []*fakeChatProcess
	launcher := func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		if len(launched) == 0 {
			t.Fatal("launcher called with no queued fake process")
		}
		p := launched[0]
		launched = launched[1:]
		return p, nil
	}
	findCLI := func() (string, error) { return "fake-monoagentcli", nil }
	sup := newChatSupervisor(store, chatSvc, launcher, func(chatevents.Event) {}, findCLI)
	return &App{aiStore: store, chatService: chatSvc, chatSup: sup}, &launched
}

type startChatTurnResponse struct {
	OK     bool   `json:"ok"`
	TurnID string `json:"turnId"`
	Status string `json:"status"`
}

func TestApp_StartChatTurn_BusyResponseIncludesTurnID(t *testing.T) {
	a, launched := newChatBindingTestApp(t)
	convJSON := a.CreateChatConversation("agent", "general", "fake-runtime", "", "")
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(convJSON), &conv); err != nil {
		t.Fatalf("unmarshal conversation: %v (%s)", err, convJSON)
	}

	*launched = append(*launched, newFakeChatProcess(""))

	resp1 := a.StartChatTurn(conv.ID, "turn-1", "hi", false, false)
	var r1 startChatTurnResponse
	if err := json.Unmarshal([]byte(resp1), &r1); err != nil {
		t.Fatalf("unmarshal first Start response: %v (%s)", err, resp1)
	}
	if !r1.OK || r1.TurnID != "turn-1" || r1.Status != "active" {
		t.Fatalf("first Start = %+v, want ok=true turnId=turn-1 status=active", r1)
	}

	resp2 := a.StartChatTurn(conv.ID, "turn-2", "hi again", false, false)
	var r2 startChatTurnResponse
	if err := json.Unmarshal([]byte(resp2), &r2); err != nil {
		t.Fatalf("unmarshal busy response: %v (%s)", err, resp2)
	}
	if r2.OK || r2.Status != "busy" {
		t.Fatalf("second Start (different turn while turn-1 active) = %+v, want ok=false status=busy", r2)
	}
	if r2.TurnID != "turn-2" {
		t.Errorf("busy response turnId = %q, want %q — frontend cannot correlate the refusal to its request without it", r2.TurnID, "turn-2")
	}
}

func TestApp_StartChatTurn_DuplicateStartResponseIncludesTurnID(t *testing.T) {
	a, launched := newChatBindingTestApp(t)
	convJSON := a.CreateChatConversation("agent", "general", "fake-runtime", "", "")
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(convJSON), &conv); err != nil {
		t.Fatalf("unmarshal conversation: %v (%s)", err, convJSON)
	}

	*launched = append(*launched, newFakeChatProcess(""))

	_ = a.StartChatTurn(conv.ID, "turn-1", "hi", false, false)
	resp := a.StartChatTurn(conv.ID, "turn-1", "hi", false, false)

	var r startChatTurnResponse
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		t.Fatalf("unmarshal duplicate-Start response: %v (%s)", err, resp)
	}
	if !r.OK || r.Status != "active" {
		t.Fatalf("duplicate Start = %+v, want ok=true status=active", r)
	}
	if r.TurnID != "turn-1" {
		t.Errorf("duplicate-Start response turnId = %q, want %q", r.TurnID, "turn-1")
	}
}

// --- cross-instance turn ownership ---
//
// docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md:
// "OwnerInstanceID is written and read back but never compared to
// anything" — two live app instances (two chatSupervisors, each with its
// own auto-generated instanceID) sharing the SAME on-disk store simulate
// two live GUI processes against one ~/.monoagent database. Deliberately
// NEVER call reconcileOrphanedTurns on either supervisor in these tests:
// that startup sweep unconditionally marks every turn left "active" in the
// store as interrupted, regardless of owner (see this file's header
// comment) — calling it here would erase the very cross-instance state
// these tests set up, and is itself the reason a real second window would
// still clobber a first window's turn at (its own) startup, which this
// fix does not close.

// newSecondInstanceApp builds an *App sharing store/chatSvc with an
// existing one but with its OWN chatSupervisor (and therefore its own
// instanceID) and launcher — the "second live process" half of a
// cross-instance test.
func newSecondInstanceApp(t *testing.T, store *ai.AIStore, chatSvc *aichat.ChatService, launcher chatProcessLauncher) *App {
	t.Helper()
	findCLI := func() (string, error) { return "fake-monoagentcli", nil }
	sup := newChatSupervisor(store, chatSvc, launcher, func(chatevents.Event) {}, findCLI)
	return &App{aiStore: store, chatService: chatSvc, chatSup: sup}
}

func neverLaunch(t *testing.T) chatProcessLauncher {
	return func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		t.Fatal("this instance's launcher must not be called")
		return nil, nil
	}
}

func TestIsForeignActiveTurn(t *testing.T) {
	cases := []struct {
		name       string
		turn       ai.Turn
		instanceID string
		want       bool
	}{
		{"active, foreign owner", ai.Turn{Status: "active", OwnerInstanceID: "other"}, "mine", true},
		{"active, own owner", ai.Turn{Status: "active", OwnerInstanceID: "mine"}, "mine", false},
		{"active, empty owner (predates ownership tracking)", ai.Turn{Status: "active", OwnerInstanceID: ""}, "mine", false},
		{"completed, foreign owner", ai.Turn{Status: "completed", OwnerInstanceID: "other"}, "mine", false},
		{"cancelled, foreign owner", ai.Turn{Status: "cancelled", OwnerInstanceID: "other"}, "mine", false},
		{"failed, foreign owner", ai.Turn{Status: "failed", OwnerInstanceID: "other"}, "mine", false},
		{"interrupted, foreign owner", ai.Turn{Status: "interrupted", OwnerInstanceID: "other"}, "mine", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isForeignActiveTurn(tc.turn, tc.instanceID); got != tc.want {
				t.Errorf("isForeignActiveTurn(%+v, %q) = %v, want %v", tc.turn, tc.instanceID, got, tc.want)
			}
		})
	}
}

// TestChatTurnListItem_JSONShape pins GetChatTurns' response shape directly:
// a promoted ai.Turn field survives embedding, the new derived field is
// present under its documented name, and the raw owner instance id (kept
// json:"-" on ai.Turn) never leaks into the response.
func TestChatTurnListItem_JSONShape(t *testing.T) {
	item := chatTurnListItem{
		Turn: ai.Turn{
			ID:              "turn-1",
			ConversationID:  "conv-1",
			ProfileID:       "default",
			OwnerInstanceID: "instance-secret",
			Status:          "active",
		},
		OwnedByThisInstance: false,
	}
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["status"] != "active" {
		t.Errorf(`m["status"] = %v, want "active" (a promoted ai.Turn field)`, m["status"])
	}
	if v, ok := m["ownedByThisInstance"].(bool); !ok || v != false {
		t.Errorf(`m["ownedByThisInstance"] = %v, want false`, m["ownedByThisInstance"])
	}
	if _, present := m["ownerInstanceId"]; present {
		t.Error("chatTurnListItem leaked an ownerInstanceId key into JSON")
	}
	if strings.Contains(string(b), "instance-secret") {
		t.Errorf("chatTurnListItem JSON leaks the raw owner instance id: %s", b)
	}
}

func TestApp_StartChatTurn_RefusedWhenAnotherLiveInstanceOwnsActiveTurn(t *testing.T) {
	store, db := newChatTestStore(t)
	chatSvc := aichat.NewChatService(store, db)
	var launchedA, launchedB []*fakeChatProcess
	launcherA := func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		if len(launchedA) == 0 {
			t.Fatal("instance A's launcher called with no queued fake process")
		}
		p := launchedA[0]
		launchedA = launchedA[1:]
		return p, nil
	}
	// Instance B's launcher must not be called for its REFUSED turn-b
	// attempt below (checked via len(launchedB) staying untouched), but
	// this test also exercises B's turn-b succeeding once A's turn goes
	// terminal — so, unlike the launcher used in the other cross-instance
	// tests below (which never expect a successful Start), B needs a real,
	// queueable launcher here too.
	launcherB := func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		if len(launchedB) == 0 {
			t.Fatal("instance B's launcher called with no queued fake process")
		}
		p := launchedB[0]
		launchedB = launchedB[1:]
		return p, nil
	}
	appA := newSecondInstanceApp(t, store, chatSvc, launcherA)
	appB := newSecondInstanceApp(t, store, chatSvc, launcherB)
	if appA.chatSup.instanceID == appB.chatSup.instanceID {
		t.Fatal("two independently constructed supervisors must have distinct instanceIDs")
	}

	convJSON := appA.CreateChatConversation("agent", "general", "fake-runtime", "", "")
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(convJSON), &conv); err != nil {
		t.Fatalf("unmarshal conversation: %v (%s)", err, convJSON)
	}

	launchedA = append(launchedA, newFakeChatProcess(""))
	respA := appA.StartChatTurn(conv.ID, "turn-a", "hi from A", false, false)
	var rA startChatTurnResponse
	if err := json.Unmarshal([]byte(respA), &rA); err != nil || !rA.OK {
		t.Fatalf("instance A's Start: resp=%s err=%v, want ok", respA, err)
	}

	// Instance B is a second live app process sharing the same store. Its
	// own local admit() succeeds (its in-memory registry has no entry for
	// this conversation — it cannot see A's process-local state), but the
	// durable CreateTurn check must refuse it: A's turn is still active.
	// Deliberately no fake process queued into launchedB yet — if the
	// refusal did NOT happen before launch, launcherB itself fails the test
	// via t.Fatal("...no queued fake process").
	respB := appB.StartChatTurn(conv.ID, "turn-b", "hi from B", false, false)
	var rawB map[string]any
	if err := json.Unmarshal([]byte(respB), &rawB); err != nil {
		t.Fatalf("unmarshal instance B's response: %v (%s)", err, respB)
	}
	if ok, _ := rawB["ok"].(bool); ok {
		t.Fatalf("instance B's Start while A's turn is active = %s, want a refusal, not a silent success", respB)
	}
	if _, hasError := rawB["error"]; !hasError {
		t.Errorf("instance B's refusal response = %s, want a clear {\"error\":...}, not a bare status", respB)
	}

	// The refusal must not wedge instance B: its in-memory admission for
	// turn-b must have been released (StartChatTurn's existing
	// CreateTurn-error path already calls release() for any error,
	// including this new one), so a future Start on this conversation from
	// B is not permanently blocked by a leaked local slot.
	if hB := appB.chatSup.lookup(conv.ID, "turn-b"); hB != nil {
		t.Error("instance B's refused turn-b is still registered in its own supervisor — a future Start on this conversation from B would wedge as permanently busy")
	}

	// Once A's turn actually finishes, B can start its own turn on the same
	// conversation — the refusal is a live-turn lock, not permanent.
	if _, _, err := store.FinalizeTurn("default", conv.ID, "turn-a", chatevents.StatusCompleted, "", nil, true); err != nil {
		t.Fatalf("FinalizeTurn(turn-a): %v", err)
	}
	launchedB = append(launchedB, newFakeChatProcess(""))
	respB2 := appB.StartChatTurn(conv.ID, "turn-b", "hi from B, retried", false, false)
	var rB2 startChatTurnResponse
	if err := json.Unmarshal([]byte(respB2), &rB2); err != nil || !rB2.OK {
		t.Fatalf("instance B's retried Start after A finished: resp=%s err=%v, want ok", respB2, err)
	}
}

func TestApp_StopChatTurn_ForeignActiveTurnReturnsExplicitFailure(t *testing.T) {
	store, db := newChatTestStore(t)
	chatSvc := aichat.NewChatService(store, db)
	var launchedA []*fakeChatProcess
	launcherA := func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		if len(launchedA) == 0 {
			t.Fatal("instance A's launcher called with no queued fake process")
		}
		p := launchedA[0]
		launchedA = launchedA[1:]
		return p, nil
	}
	appA := newSecondInstanceApp(t, store, chatSvc, launcherA)
	appB := newSecondInstanceApp(t, store, chatSvc, neverLaunch(t))

	convJSON := appA.CreateChatConversation("agent", "general", "fake-runtime", "", "")
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(convJSON), &conv); err != nil {
		t.Fatalf("unmarshal conversation: %v (%s)", err, convJSON)
	}

	launchedA = append(launchedA, newFakeChatProcess(""))
	respA := appA.StartChatTurn(conv.ID, "turn-a", "hi from A", false, false)
	var rA startChatTurnResponse
	if err := json.Unmarshal([]byte(respA), &rA); err != nil || !rA.OK {
		t.Fatalf("instance A's Start failed: %s", respA)
	}

	// Instance B has no local handle for turn-a (it belongs to A's own
	// in-memory registry) but the durable store says it's active, owned by
	// A. Stopping it from B must not silently no-op {"ok":true}.
	respB := appB.StopChatTurn(conv.ID, "turn-a")
	var stopResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(respB), &stopResp); err != nil {
		t.Fatalf("unmarshal StopChatTurn response: %v (%s)", err, respB)
	}
	if stopResp.OK {
		t.Fatalf("instance B's StopChatTurn on A's active turn = %s, want an explicit failure, not a silent {\"ok\":true} no-op", respB)
	}
	if stopResp.Error == "" {
		t.Error("StopChatTurn's foreign-turn failure carries no error message")
	}

	// The turn itself must be unaffected: still active — B's refused Stop
	// must not have touched it.
	turn, err := store.GetTurn("turn-a", "default")
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if turn.Status != "active" {
		t.Errorf("turn status after B's refused Stop = %q, want still %q", turn.Status, "active")
	}

	// Sanity: A itself can still stop its own turn normally — this fix must
	// not have disturbed the same-instance path.
	respAStop := appA.StopChatTurn(conv.ID, "turn-a")
	var okOnly struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(respAStop), &okOnly); err != nil || !okOnly.OK {
		t.Errorf("instance A stopping its own turn: %s (err=%v), want ok:true", respAStop, err)
	}
}

// TestApp_StopChatTurn_UnknownIDAndFinishedTurnRemainNoOpSuccess guards the
// two cases StopChatTurn's contract explicitly keeps as a harmless no-op —
// only the foreign-active case (tested above) gets the new explicit
// failure.
func TestApp_StopChatTurn_UnknownIDAndFinishedTurnRemainNoOpSuccess(t *testing.T) {
	store, db := newChatTestStore(t)
	chatSvc := aichat.NewChatService(store, db)
	a := newSecondInstanceApp(t, store, chatSvc, neverLaunch(t))

	resp := a.StopChatTurn("conv-nonexistent", "turn-nonexistent")
	var r struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil || !r.OK {
		t.Fatalf("StopChatTurn on an unknown id = %s (err=%v), want ok:true (unchanged idempotent no-op)", resp, err)
	}

	convJSON := a.CreateChatConversation("agent", "general", "fake-runtime", "", "")
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(convJSON), &conv); err != nil {
		t.Fatalf("unmarshal conversation: %v (%s)", err, convJSON)
	}
	if _, _, err := store.CreateTurn(conv.ID, "default", "turn-done", a.chatSup.instanceID, "hi"); err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if _, _, err := store.FinalizeTurn("default", conv.ID, "turn-done", chatevents.StatusCompleted, "", nil, true); err != nil {
		t.Fatalf("FinalizeTurn: %v", err)
	}

	resp2 := a.StopChatTurn(conv.ID, "turn-done")
	var r2 struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(resp2), &r2); err != nil || !r2.OK {
		t.Fatalf("StopChatTurn on an already-finished OWN turn = %s (err=%v), want ok:true", resp2, err)
	}
}

func TestApp_GetChatTurns_OwnedByThisInstance(t *testing.T) {
	store, db := newChatTestStore(t)
	chatSvc := aichat.NewChatService(store, db)
	var launchedA []*fakeChatProcess
	launcherA := func(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
		if len(launchedA) == 0 {
			t.Fatal("instance A's launcher called with no queued fake process")
		}
		p := launchedA[0]
		launchedA = launchedA[1:]
		return p, nil
	}
	appA := newSecondInstanceApp(t, store, chatSvc, launcherA)
	appB := newSecondInstanceApp(t, store, chatSvc, neverLaunch(t))

	convJSON := appA.CreateChatConversation("agent", "general", "fake-runtime", "", "")
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(convJSON), &conv); err != nil {
		t.Fatalf("unmarshal conversation: %v (%s)", err, convJSON)
	}

	// A previous, finished turn — ordinary history. It must read as owned
	// (never read-only) from EITHER instance's view, regardless of who
	// actually ran it.
	if _, _, err := store.CreateTurn(conv.ID, "default", "turn-old", appB.chatSup.instanceID, "old, by B"); err != nil {
		t.Fatalf("CreateTurn(turn-old): %v", err)
	}
	if _, _, err := store.FinalizeTurn("default", conv.ID, "turn-old", chatevents.StatusCompleted, "", nil, true); err != nil {
		t.Fatalf("FinalizeTurn(turn-old): %v", err)
	}

	// A's own currently-active turn.
	launchedA = append(launchedA, newFakeChatProcess(""))
	respA := appA.StartChatTurn(conv.ID, "turn-active-a", "hi from A", false, false)
	var rA startChatTurnResponse
	if err := json.Unmarshal([]byte(respA), &rA); err != nil || !rA.OK {
		t.Fatalf("instance A's Start failed: %s", respA)
	}

	type turnItem struct {
		ID                  string `json:"id"`
		Status              string `json:"status"`
		OwnedByThisInstance bool   `json:"ownedByThisInstance"`
	}
	byID := func(items []turnItem, id string) (turnItem, bool) {
		for _, it := range items {
			if it.ID == id {
				return it, true
			}
		}
		return turnItem{}, false
	}

	// From A's own view: both the finished turn and A's own active turn
	// must read as owned.
	var gotA struct {
		Items []turnItem `json:"items"`
	}
	respGetA := appA.GetChatTurns(conv.ID, "", 50)
	if err := json.Unmarshal([]byte(respGetA), &gotA); err != nil {
		t.Fatalf("unmarshal GetChatTurns(A): %v (%s)", err, respGetA)
	}
	if old, ok := byID(gotA.Items, "turn-old"); !ok || !old.OwnedByThisInstance {
		t.Errorf("A's view of finished turn-old: %+v ok=%v, want ownedByThisInstance=true (finished turns are never read-only)", old, ok)
	}
	if activeA, ok := byID(gotA.Items, "turn-active-a"); !ok || !activeA.OwnedByThisInstance {
		t.Errorf("A's view of its OWN active turn: %+v ok=%v, want ownedByThisInstance=true", activeA, ok)
	}
	if strings.Contains(respGetA, appA.chatSup.instanceID) || strings.Contains(respGetA, appB.chatSup.instanceID) {
		t.Errorf("GetChatTurns(A) response leaks a raw instance ID: %s", respGetA)
	}

	// From B's view: the finished turn is still owned=true (history is
	// never read-only), but A's currently-active turn must read as
	// owned=false — the one case the contract calls out.
	var gotB struct {
		Items []turnItem `json:"items"`
	}
	respGetB := appB.GetChatTurns(conv.ID, "", 50)
	if err := json.Unmarshal([]byte(respGetB), &gotB); err != nil {
		t.Fatalf("unmarshal GetChatTurns(B): %v (%s)", err, respGetB)
	}
	if oldB, ok := byID(gotB.Items, "turn-old"); !ok || !oldB.OwnedByThisInstance {
		t.Errorf("B's view of finished turn-old: %+v ok=%v, want ownedByThisInstance=true", oldB, ok)
	}
	if activeFromB, ok := byID(gotB.Items, "turn-active-a"); !ok || activeFromB.OwnedByThisInstance {
		t.Errorf("B's view of A's currently-active turn: %+v ok=%v, want ownedByThisInstance=FALSE (the one case that must read read-only)", activeFromB, ok)
	}
	if strings.Contains(respGetB, appA.chatSup.instanceID) || strings.Contains(respGetB, appB.chatSup.instanceID) {
		t.Errorf("GetChatTurns(B) response leaks a raw instance ID: %s", respGetB)
	}
}
