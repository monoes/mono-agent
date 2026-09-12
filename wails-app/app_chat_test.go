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
func (p *fakeChatProcess) Stderr() io.Reader { return strings.NewReader(p.stderr) }

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
