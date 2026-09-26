package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// --- test helpers ---

// jsonUnmarshalPayload decodes one event's payload into target.
func jsonUnmarshalPayload(ev chatevents.Event, target any) error {
	return json.Unmarshal(ev.Payload, target)
}

// collectingEmitter records every emitted event, safe for concurrent use.
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

// waitForType polls until at least one event of typ has been emitted, or
// fails the test after timeout.
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

// waitReleased polls until the turn's admission slot is freed.
func waitReleased(t *testing.T, sup *chatSupervisor, conversationID, turnID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for sup.lookup(conversationID, turnID) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("turn %s still registered", turnID)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// fakeChatProcess is an injectable chatProcess standing in for a
// `monoagentcli chat --conversation` process. Stdout is an io.Pipe so a
// test controls exactly what arrives and when EOF happens.
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

// writeLine pushes one NDJSON line to the stdout reader (in the background,
// since an unread io.Pipe write blocks).
func (p *fakeChatProcess) writeLine(s string) {
	_, _ = p.stdoutW.Write([]byte(s + "\n"))
}

// endStream simulates the process exiting on its own.
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
	_ = p.stdoutW.CloseWithError(io.EOF)
	p.exitOnce.Do(func() { close(p.exited) })
}

func (p *fakeChatProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// queueLauncher hands out queued fake processes and records each argv.
type queueLauncher struct {
	t     *testing.T
	mu    sync.Mutex
	procs []*fakeChatProcess
	argvs [][]string
}

func (q *queueLauncher) push(p *fakeChatProcess) {
	q.mu.Lock()
	q.procs = append(q.procs, p)
	q.mu.Unlock()
}

func (q *queueLauncher) launch(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.argvs = append(q.argvs, args)
	if len(q.procs) == 0 {
		q.t.Error("launcher called with no queued fake process")
		return nil, errors.New("no fake process")
	}
	p := q.procs[0]
	q.procs = q.procs[1:]
	return p, nil
}

// newTestSupervisor wires a supervisor to a fake launcher and emitter. Its
// CLI path names no real binary, so a history call fails (see
// app_chat_cli_test.go for those).
func newTestSupervisor(t *testing.T) (*chatSupervisor, *collectingEmitter, *queueLauncher) {
	t.Helper()
	emitter := &collectingEmitter{}
	q := &queueLauncher{t: t}
	findCLI := func() (string, error) { return "fake-monoagentcli-not-on-disk", nil }
	return newChatSupervisor(q.launch, emitter.emit, findCLI), emitter, q
}

// admissionLine / eventLine build the turn process's stdout lines.
func admissionLine(conversationID, turnID, status string, existed bool) string {
	b, _ := json.Marshal(chatAdmission{Admitted: !existed, Existed: existed, Turn: ai.TurnRecord{
		ID: turnID, ConversationID: conversationID, ProfileID: "default", Status: status,
	}})
	return string(b)
}

func eventLine(conversationID, turnID string, seq int64, typ chatevents.EventType, payload any) string {
	ev, _ := chatevents.New("default", conversationID, turnID, seq, time.Now(), typ, payload)
	b, _ := json.Marshal(ev.Record())
	return string(b)
}

// startFakeTurn admits turnID and starts it against proc, feeding the
// admission line so startTurn returns.
func startFakeTurn(t *testing.T, sup *chatSupervisor, q *queueLauncher, proc *fakeChatProcess, conversationID, turnID string) string {
	t.Helper()
	q.push(proc)
	h, _, err := sup.admit(conversationID, turnID)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	h.profileID = "default"
	go proc.writeLine(admissionLine(conversationID, turnID, "active", false))
	resp, err := sup.startTurn(h, "hi", false, false)
	if err != nil {
		t.Fatalf("startTurn: %v", err)
	}
	return resp
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

// --- relaying a turn process ---

func TestChatSupervisor_RelaysCommittedEventsInOrder(t *testing.T) {
	sup, emitter, q := newTestSupervisor(t)
	proc := newFakeChatProcess("")
	resp := startFakeTurn(t, sup, q, proc, "conv-1", "turn-1")
	if resp != `{"ok":true,"turnId":"turn-1","status":"active"}` {
		t.Fatalf("startTurn = %s", resp)
	}
	wantArgv := "--profile default chat --conversation conv-1 --turn turn-1 --instance " + sup.instanceID + " -- hi"
	if got := strings.Join(q.argvs[0], " "); got != wantArgv {
		t.Errorf("argv = %q\nwant   %q", got, wantArgv)
	}

	go func() {
		proc.writeLine(eventLine("conv-1", "turn-1", 1, chatevents.EventTurnStarted, chatevents.TurnStartedPayload{Backend: "agent", Text: "hi"}))
		proc.writeLine("not json: a stray progress line")
		proc.writeLine(eventLine("conv-1", "turn-1", 2, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: "part-1", Text: "hello"}))
		proc.writeLine(eventLine("conv-1", "turn-1", 3, chatevents.EventTurnFinished, chatevents.TurnFinishedPayload{Status: chatevents.StatusCompleted, HistorySaved: true}))
		proc.endStream(nil)
	}()

	finished := waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	var payload chatevents.TurnFinishedPayload
	jsonUnmarshalPayload(finished[0], &payload)
	if payload.Status != chatevents.StatusCompleted || !payload.HistorySaved {
		t.Errorf("turn.finished = %+v", payload)
	}
	waitReleased(t, sup, "conv-1", "turn-1")
	all := emitter.snapshot()
	if len(all) != 3 || all[0].Type != chatevents.EventTurnStarted || all[0].Seq != 1 || all[1].Seq != 2 || all[2].Seq != 3 {
		t.Fatalf("emitted = %+v", all)
	}
	if all[1].ConversationID != "conv-1" || all[1].TurnID != "turn-1" || all[1].ProfileID != "default" {
		t.Errorf("event envelope not converted back: %+v", all[1])
	}
	// The frontend's event shape: camelCase envelope, payload verbatim.
	b, _ := json.Marshal(all[1])
	if !strings.Contains(string(b), `"conversationId":"conv-1"`) || !strings.Contains(string(b), `"partId":"part-1"`) {
		t.Errorf("emitted JSON = %s", b)
	}
}

func TestChatSupervisor_ToolsFlagInArgv(t *testing.T) {
	for _, tc := range []struct {
		tools, allowRuns bool
		want             string
	}{
		{false, false, ""},
		{true, false, "--tools monoagent "},
		{true, true, "--tools monoagent,runs "},
	} {
		got := strings.Join(chatTurnArgs("p", "c", "t", "i", "-x history", tc.tools, tc.allowRuns), " ")
		want := "--profile p chat --conversation c --turn t --instance i " + tc.want + "-- -x history"
		if got != want {
			t.Errorf("chatTurnArgs(tools=%v, runs=%v) = %q, want %q", tc.tools, tc.allowRuns, got, want)
		}
	}
}

func TestChatSupervisor_StaleStopAfterCompletionIsNoOp(t *testing.T) {
	sup, emitter, q := newTestSupervisor(t)
	proc := newFakeChatProcess("")
	startFakeTurn(t, sup, q, proc, "conv-1", "turn-done")
	h := sup.lookup("conv-1", "turn-done")
	go func() {
		proc.writeLine(eventLine("conv-1", "turn-done", 1, chatevents.EventTurnFinished, chatevents.TurnFinishedPayload{Status: chatevents.StatusCompleted, HistorySaved: true}))
		proc.endStream(nil)
	}()
	waitForType(t, emitter, chatevents.EventTurnFinished, 2*time.Second)
	waitReleased(t, sup, "conv-1", "turn-done")
	h.requestStop() // must not panic, and nothing more is emitted
	time.Sleep(20 * time.Millisecond)
	if n := len(emitter.snapshot()); n != 1 {
		t.Errorf("a stale Stop emitted more events (%d)", n)
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

// TestChatTurnListItem_JSONShape pins GetChatTurns' item shape: promoted
// ai.Turn fields survive embedding, the derived flag is present, and the
// raw owner instance id (json:"-" on ai.Turn) never leaks.
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
	if m["status"] != "active" || m["conversationId"] != "conv-1" {
		t.Errorf("promoted ai.Turn fields missing: %s", b)
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
