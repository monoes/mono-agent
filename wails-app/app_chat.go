package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// Chat supervisor (docs/mastermind/plans/2026-09-11-interactive-agent-chat.md)
//
// The chat's history lives behind `monoagentcli chat` (issue #172): every
// read, write and delete of conversations, turns and events is a CLI call,
// and a turn is one `monoagentcli chat --conversation C --turn T -- <msg>`
// process that creates its turn row and journals its own events. What stays
// here is supervising those processes, which only the UI process can do:
// admission (one active turn per conversation), the process group a Stop
// kills, the instance id that tells this window's turns from another
// window's, relaying each committed event to the frontend as "chat:event",
// and finishing a turn (through `chat history finish`) whose process died
// before it could.
//
// Startup reconciliation (`chat history reconcile`) marks every turn left
// active as interrupted, except this instance's own. It cannot tell a dead
// previous instance from a second live one (no liveness signal exists); for
// a single-user desktop app sharing one SQLite file that is an edge case.
// ─────────────────────────────────────────────────────────────────────────────

// chatProcess abstracts a running `monoagentcli chat` subprocess so tests
// can inject a fake one without a real binary. Stdout/Stderr must be safe
// to read concurrently with each other (they are, in the real
// exec.Cmd-backed implementation: separate pipes).
type chatProcess interface {
	Stdout() io.Reader
	Stderr() io.Reader
	// Wait blocks until the process exits and returns its error (nil on a
	// clean exit), mirroring exec.Cmd.Wait.
	Wait() error
	// Kill terminates the process (and, where supported, its process
	// group) immediately. Safe to call after the process has already
	// exited.
	Kill()
}

// realChatProcess wraps a real *exec.Cmd, using the process-group helpers
// in proc_unix.go/proc_windows.go.
type realChatProcess struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (p *realChatProcess) Stdout() io.Reader { return p.stdout }
func (p *realChatProcess) Stderr() io.Reader { return p.stderr }
func (p *realChatProcess) Wait() error       { return p.cmd.Wait() }
func (p *realChatProcess) Kill()             { killChatProcessGroup(p.cmd) }

// chatProcessLauncher starts one `monoagentcli chat ...` turn. Injectable so
// tests can drive the supervisor against a fake process.
type chatProcessLauncher func(ctx context.Context, cliBin string, args []string) (chatProcess, error)

func defaultChatProcessLauncher(ctx context.Context, cliBin string, args []string) (chatProcess, error) {
	cmd := exec.CommandContext(ctx, cliBin, args...)
	setChatProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &realChatProcess{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

// chatEventEmitter delivers one committed event live to the frontend.
// Injectable so tests never need a real Wails runtime context.
type chatEventEmitter func(ev chatevents.Event)

// chatTurnKey identifies one turn's registry slot.
type chatTurnKey struct{ conversationID, turnID string }

// chatTurnHandle is the in-memory state for one admitted, currently-active
// turn, removed from the registry once the turn finishes.
type chatTurnHandle struct {
	profileID      string // captured at admission, immutable for the turn's life
	conversationID string
	turnID         string

	cancel context.CancelFunc

	mu            sync.Mutex
	stopRequested bool
	proc          chatProcess // nil until the subprocess is launched
}

func (h *chatTurnHandle) requestStop() {
	h.mu.Lock()
	already := h.stopRequested
	h.stopRequested = true
	proc := h.proc
	cancel := h.cancel
	h.mu.Unlock()
	if already {
		return
	}
	if cancel != nil {
		cancel()
	}
	if proc != nil {
		proc.Kill()
	}
}

func (h *chatTurnHandle) isStopRequested() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopRequested
}

// chatCLIError is a failed `monoagentcli` call: its exit code and the error
// line it printed.
type chatCLIError struct {
	code int
	msg  string
}

func (e *chatCLIError) Error() string { return e.msg }

// chatCLIExitCode returns err's CLI exit code, or 0 when err isn't one.
func chatCLIExitCode(err error) int {
	var ce *chatCLIError
	if errors.As(err, &ce) {
		return ce.code
	}
	return 0
}

// lastLine returns s's last non-empty line: the CLI prints its error last,
// after any warnings.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// chatCLITimeout bounds one history call (the CLI migrates the DB on open).
const chatCLITimeout = 60 * time.Second

// chatAdmissionTimeout bounds how long StartChatTurn waits for the turn
// process to register its turn.
const chatAdmissionTimeout = 60 * time.Second

// chatSupervisor owns admission and the per-turn registry. Independently
// constructible (no Wails runtime/App required) so it is fully
// unit-testable — see app_chat_test.go.
type chatSupervisor struct {
	launcher chatProcessLauncher
	emit     chatEventEmitter
	findCLI  func() (string, error)
	// instanceID identifies this running app process, recorded as a turn's
	// owner — distinguishes "my own turn, stoppable here" from "another
	// live instance's turn, read-only here" (plan §236). Fresh per process
	// start.
	instanceID string

	mu                   sync.Mutex
	turns                map[chatTurnKey]*chatTurnHandle
	activeByConversation map[string]chatTurnKey // one active turn per conversation
	running              sync.WaitGroup         // turn relays still finishing
}

func newChatSupervisor(launcher chatProcessLauncher, emit chatEventEmitter, findCLI func() (string, error)) *chatSupervisor {
	return &chatSupervisor{
		launcher:             launcher,
		emit:                 emit,
		findCLI:              findCLI,
		instanceID:           uuid.NewString(),
		turns:                make(map[chatTurnKey]*chatTurnHandle),
		activeByConversation: make(map[string]chatTurnKey),
	}
}

// cli runs `monoagentcli [--profile P] --json <args>` and decodes its stdout
// into result (when non-nil). profileID "" leaves --profile off.
func (sup *chatSupervisor) cli(profileID string, result any, args ...string) error {
	bin, err := sup.findCLI()
	if err != nil {
		return err
	}
	full := []string{"--json"}
	if profileID != "" {
		full = []string{"--profile", profileID, "--json"}
	}
	full = append(full, args...)
	ctx, cancel := context.WithTimeout(context.Background(), chatCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, full...)
	hideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := lastLine(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return &chatCLIError{code: ee.ExitCode(), msg: msg}
		}
		return err
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(out, result); err != nil {
		return fmt.Errorf("monoagentcli %s: unexpected output: %w", strings.Join(args, " "), err)
	}
	return nil
}

// reconcileOrphanedTurns marks every turn left active by a previous
// instance as interrupted, via `chat history reconcile`. This instance's
// own turns are skipped, so it may run concurrently with the first Start.
func (sup *chatSupervisor) reconcileOrphanedTurns() []error {
	if err := sup.cli("", nil, "chat", "history", "reconcile", "--except-owner", sup.instanceID); err != nil {
		return []error{err}
	}
	return nil
}

// ─── Admission ──────────────────────────────────────────────────────────────

var errChatBusy = fmt.Errorf("busy")

// admit registers turnID as the active turn for conversationID, or reports
// busy/duplicate. Returns the existing handle (idempotent re-admission) when
// turnID matches the conversation's already-active turn.
func (sup *chatSupervisor) admit(conversationID, turnID string) (h *chatTurnHandle, alreadyActive bool, err error) {
	sup.mu.Lock()
	defer sup.mu.Unlock()
	if key, ok := sup.activeByConversation[conversationID]; ok {
		if key.turnID == turnID {
			return sup.turns[key], true, nil
		}
		return nil, false, errChatBusy
	}
	key := chatTurnKey{conversationID: conversationID, turnID: turnID}
	h = &chatTurnHandle{conversationID: conversationID, turnID: turnID}
	sup.turns[key] = h
	sup.activeByConversation[conversationID] = key
	return h, false, nil
}

// release clears a finished turn's admission slot.
func (sup *chatSupervisor) release(conversationID, turnID string) {
	sup.mu.Lock()
	defer sup.mu.Unlock()
	key := chatTurnKey{conversationID: conversationID, turnID: turnID}
	delete(sup.turns, key)
	if cur, ok := sup.activeByConversation[conversationID]; ok && cur == key {
		delete(sup.activeByConversation, conversationID)
	}
}

func (sup *chatSupervisor) lookup(conversationID, turnID string) *chatTurnHandle {
	sup.mu.Lock()
	defer sup.mu.Unlock()
	return sup.turns[chatTurnKey{conversationID: conversationID, turnID: turnID}]
}

// stopAll requests stop on every registered turn — called at app shutdown
// so no orphaned subprocess survives the GUI (plan §236: "App shutdown
// cancels its registered turns") — and gives their relays a few seconds to
// record the cancellation. A turn still unfinished after that is caught by
// the next startup's reconcile.
func (sup *chatSupervisor) stopAll() {
	sup.mu.Lock()
	handles := make([]*chatTurnHandle, 0, len(sup.turns))
	for _, h := range sup.turns {
		handles = append(handles, h)
	}
	sup.mu.Unlock()
	for _, h := range handles {
		h.requestStop()
	}
	done := make(chan struct{})
	go func() { sup.running.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// ─── Turn process ───────────────────────────────────────────────────────────

// chatAdmission is the turn process's first stdout line.
type chatAdmission struct {
	Admitted bool          `json:"admitted"`
	Existed  bool          `json:"existed"`
	Turn     ai.TurnRecord `json:"turn"`
}

// chatTurnArgs builds the turn process's argv. --json makes a turn that
// fails before admission print {"error","code"} as its only stdout line.
// A coder conversation's turn is started with tools=false: the CLI takes
// its mode and folder from the conversation and refuses --tools for it.
func chatTurnArgs(profileID, conversationID, turnID, instanceID, message string, tools, allowRuns bool) []string {
	args := []string{"--profile", profileID, "--json", "chat", "--conversation", conversationID, "--turn", turnID, "--instance", instanceID}
	if tools {
		toolsFlag := "monoagent"
		if allowRuns {
			toolsFlag = "monoagent,runs"
		}
		args = append(args, "--tools", toolsFlag)
	}
	return append(args, "--", message)
}

// turnOutput is what a turn process's pipes produced: its JSON stdout lines
// in order, then (after close) its stderr, exit error and exit code.
type turnOutput struct {
	lines      chan []byte
	stderrDone chan struct{}
	stderr     *strings.Builder
}

func readTurnOutput(proc chatProcess) *turnOutput {
	o := &turnOutput{lines: make(chan []byte, 256), stderrDone: make(chan struct{}), stderr: &strings.Builder{}}
	go func() {
		defer close(o.stderrDone)
		_, _ = io.Copy(o.stderr, proc.Stderr())
	}()
	go func() {
		defer close(o.lines)
		sc := bufio.NewScanner(proc.Stdout())
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			o.lines <- append([]byte(nil), line...)
		}
	}()
	return o
}

// exited waits for the process after stdout closed, then for stderr to be
// fully drained (Wait is what force-closes a pipe a straggling grandchild
// still holds, so stderr is joined after it, not before).
func (o *turnOutput) exited(proc chatProcess) (waitErr error, stderr string) {
	waitErr = proc.Wait()
	<-o.stderrDone
	return waitErr, o.stderr.String()
}

// startTurn launches the turn process and waits for its admission line.
// It returns the StartChatTurn response; on success the turn keeps
// streaming in the background.
func (sup *chatSupervisor) startTurn(h *chatTurnHandle, message string, tools, allowRuns bool) (string, error) {
	cliBin, err := sup.findCLI()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	h.cancel = cancel
	h.mu.Unlock()
	proc, err := sup.launcher(ctx, cliBin, chatTurnArgs(h.profileID, h.conversationID, h.turnID, sup.instanceID, message, tools, allowRuns))
	if err != nil {
		cancel()
		return "", err
	}
	h.mu.Lock()
	h.proc = proc
	stopped := h.stopRequested
	h.mu.Unlock()
	if stopped {
		proc.Kill()
	}

	out := readTurnOutput(proc)
	var adm chatAdmission
	select {
	case line, ok := <-out.lines:
		var refusal chatRefusal
		if ok {
			err = json.Unmarshal(line, &adm)
			_ = json.Unmarshal(line, &refusal)
		}
		if !ok || err != nil || adm.Turn.ID == "" {
			proc.Kill()
			for range out.lines {
			}
			waitErr, stderr := out.exited(proc)
			cancel()
			if h.isStopRequested() {
				// Stopped while starting: the process may have registered
				// the turn before it died.
				sup.finishStoppedStart(h)
			}
			return "", admissionError(waitErr, stderr, refusal)
		}
	case <-time.After(chatAdmissionTimeout):
		proc.Kill()
		go func() {
			for range out.lines {
			}
			_, _ = out.exited(proc)
		}()
		cancel()
		return "", fmt.Errorf("monoagentcli did not start the turn within %s", chatAdmissionTimeout)
	}

	if adm.Existed {
		// This exact turn id already ran (e.g. a Start retried after a
		// restart): the process reports it and exits without running it.
		go func() {
			for range out.lines {
			}
			_, _ = out.exited(proc)
			cancel()
		}()
		sup.release(h.conversationID, h.turnID)
		b, _ := json.Marshal(map[string]any{"ok": true, "turnId": h.turnID, "status": adm.Turn.Status})
		return string(b), nil
	}

	sup.running.Add(1)
	go func() {
		defer sup.running.Done()
		defer cancel()
		sup.relayTurn(h, proc, out)
	}()
	return fmt.Sprintf(`{"ok":true,"turnId":%q,"status":"active"}`, h.turnID), nil
}

// chatRefusal is the {"error","code"} line a turn process prints (under
// --json) when it fails before admitting its turn.
type chatRefusal struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// codedError is an error the CLI classified: its code travels to the
// frontend next to the message (e.g. agent_not_setup).
type codedError struct {
	msg  string
	code string
}

func (e *codedError) Error() string { return e.msg }

// admissionError explains a turn process that exited without admitting
// its turn, keeping the CLI's error code when it printed one. An older
// monoagentcli that predates --conversation is called out, since it would
// otherwise read like any other failure.
func admissionError(waitErr error, stderr string, refusal chatRefusal) error {
	if strings.Contains(stderr, "unknown flag") {
		return fmt.Errorf("the installed monoagentcli does not recognize a flag this app requires (%s); update monoagentcli to match this app's version", lastLine(stderr))
	}
	msg := lastLine(stderr)
	if msg == "" {
		msg = refusal.Error // the process was killed before its stderr
	}
	if msg != "" {
		if refusal.Code != "" {
			return &codedError{msg: msg, code: refusal.Code}
		}
		return errors.New(msg)
	}
	if waitErr != nil {
		return fmt.Errorf("monoagentcli exited before starting the turn: %w", waitErr)
	}
	return errors.New("monoagentcli exited before starting the turn")
}

// relayTurn emits each committed event the turn process prints, in order,
// and finishes the turn itself if the process ends without having done so.
func (sup *chatSupervisor) relayTurn(h *chatTurnHandle, proc chatProcess, out *turnOutput) {
	finished := false
	for line := range out.lines {
		var rec chatevents.Record
		if json.Unmarshal(line, &rec) != nil || rec.Type == "" {
			continue
		}
		if rec.Type == chatevents.EventTurnFinished {
			finished = true
		}
		if sup.emit != nil {
			sup.emit(rec.Event())
		}
	}
	waitErr, stderr := out.exited(proc)
	if !finished {
		sup.finishOrphanedTurn(h, waitErr, stderr)
	}
	sup.release(h.conversationID, h.turnID)
}

type chatFinishResult struct {
	Finalized bool               `json:"finalized"`
	Event     *chatevents.Record `json:"event"`
}

// finishOrphanedTurn records the end of a turn whose process exited without
// its turn.finished — killed by Stop, crashed, or cut off — through
// `chat history finish`, and emits the committed event. If that fails too,
// the UI still gets a live-only turn.finished (seq MaxSafeSeq so the
// frontend never drops it as stale, historySaved false) instead of hanging.
func (sup *chatSupervisor) finishOrphanedTurn(h *chatTurnHandle, waitErr error, stderr string) {
	status, reason := chatevents.ComputeTurnStatus(h.isStopRequested(), nil)
	var exitCode *int
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) && ee.ExitCode() >= 0 {
		v := ee.ExitCode()
		exitCode = &v
	}
	if !h.isStopRequested() {
		if waitErr != nil {
			status, reason = chatevents.StatusFailed, "monoagentcli exited: "+waitErr.Error()
			if msg := lastLine(stderr); msg != "" {
				reason += ": " + msg
			}
		} else {
			reason = "monoagentcli ended without finishing the turn"
		}
	}
	args := []string{"chat", "history", "finish", h.conversationID, h.turnID, "--status", string(status), "--reason", reason}
	if exitCode != nil {
		args = append(args, "--exit-code", strconv.Itoa(*exitCode))
	}
	var res chatFinishResult
	if err := sup.cli(h.profileID, &res, args...); err != nil {
		live, buildErr := chatevents.New(h.profileID, h.conversationID, h.turnID, chatevents.MaxSafeSeq, time.Now(), chatevents.EventTurnFinished, chatevents.TurnFinishedPayload{
			Status: status, Reason: reason, ExitCode: exitCode, HistorySaved: false,
		})
		if buildErr == nil && sup.emit != nil {
			sup.emit(live)
		}
		return
	}
	if res.Finalized && res.Event != nil && sup.emit != nil {
		sup.emit(res.Event.Event())
	}
}

// finishStoppedStart records a turn stopped before its process admitted it
// as cancelled, if the process got as far as creating it. Unlike
// finishOrphanedTurn it emits nothing when there is no such turn.
func (sup *chatSupervisor) finishStoppedStart(h *chatTurnHandle) {
	status, reason := chatevents.ComputeTurnStatus(true, nil)
	var res chatFinishResult
	if sup.cli(h.profileID, &res, "chat", "history", "finish", h.conversationID, h.turnID, "--status", string(status), "--reason", reason) == nil &&
		res.Finalized && res.Event != nil && sup.emit != nil {
		sup.emit(res.Event.Event())
	}
}

// ─── Wails bindings ─────────────────────────────────────────────────────────
//
// Contracts per the plan (§175), unchanged by the move to the CLI: the
// CLI's snake_case records are converted back to the ai/chatevents types
// whose JSON these bindings always returned. Responses follow the existing
// JSON-string convention: {"error":...} on failure, otherwise a typed
// payload.

func (a *App) chatBindingError(err error) string { return aiError(err) }

// isForeignActiveTurn reports whether t is an active turn owned by a
// DIFFERENT, identified live instance than instanceID — the one case in
// which another app instance's turn (sharing this database) is read-only
// here: it drives GetChatTurns' ownedByThisInstance and StopChatTurn's
// explicit foreign-turn failure. A terminal turn is never foreign, and
// neither is an active turn with no recorded owner (a row predating
// ownership), which falls back to "treat as mine".
func isForeignActiveTurn(t ai.Turn, instanceID string) bool {
	return t.Status == "active" && t.OwnerInstanceID != "" && t.OwnerInstanceID != instanceID
}

// chatTurnListItem is one GetChatTurns response entry: the stored turn plus
// a derived, instance-scoped ownership flag. ai.Turn.OwnerInstanceID stays
// json:"-" (never exposed raw).
type chatTurnListItem struct {
	ai.Turn
	OwnedByThisInstance bool `json:"ownedByThisInstance"`
}

// CreateChatConversation creates a new scoped agent conversation.
// workflowID is the tool/ownership context ("general"/"draft"/an owned
// workflow id).
func (a *App) CreateChatConversation(workflowID, runtimeID, model string) string {
	if a.chatSup == nil {
		return a.chatBindingError(fmt.Errorf("chat supervisor not initialized"))
	}
	args := []string{"chat", "history", "create", "--runtime", runtimeID, "--workflow", workflowID}
	if model != "" {
		args = append(args, "--model", model)
	}
	var rec ai.ConversationRecord
	if err := a.chatSup.cli(a.getActiveProfileID(), &rec, args...); err != nil {
		return a.chatBindingError(err)
	}
	b, _ := json.Marshal(rec.Conversation())
	return string(b)
}

// StartChatTurn admits and starts one turn. Duplicate calls with the same
// turnID against an already-admitted turn return the existing admission
// (idempotent Start); a different turnID while one is active returns busy.
// Every response carries turnId (plan §189).
func (a *App) StartChatTurn(conversationID, turnID, message string, tools, allowRuns bool) string {
	if a.chatSup == nil {
		return a.chatBindingError(fmt.Errorf("chat supervisor not initialized"))
	}
	h, alreadyActive, err := a.chatSup.admit(conversationID, turnID)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"turnId":%q,"status":"busy"}`, turnID)
	}
	if alreadyActive {
		return fmt.Sprintf(`{"ok":true,"turnId":%q,"status":"active"}`, turnID)
	}
	h.profileID = a.getActiveProfileID()
	resp, err := a.chatSup.startTurn(h, message, tools, allowRuns)
	if err != nil {
		a.chatSup.release(conversationID, turnID)
		return a.chatBindingError(err)
	}
	return resp
}

// StopChatTurn requests cancellation of a turn. Idempotent: stopping an
// already-finished turn, or an unknown turn id, is a harmless no-op
// success. A turn that is ACTIVE but owned by a different live instance is
// neither: nothing here would stop it, so that case reports an explicit
// failure instead. See isForeignActiveTurn.
func (a *App) StopChatTurn(conversationID, turnID string) string {
	if a.chatSup == nil {
		return a.chatBindingError(fmt.Errorf("chat supervisor not initialized"))
	}
	if h := a.chatSup.lookup(conversationID, turnID); h != nil {
		h.requestStop()
		return `{"ok":true}`
	}
	var rec ai.TurnRecord
	if err := a.chatSup.cli(a.getActiveProfileID(), &rec, "chat", "history", "turn", conversationID, turnID); err == nil {
		if isForeignActiveTurn(rec.Turn(), a.chatSup.instanceID) {
			return `{"ok":false,"error":"this turn is running in another window and can only be stopped there"}`
		}
	}
	return `{"ok":true}`
}

type chatConversationPage struct {
	Items      []ai.ConversationRecord `json:"items"`
	NextCursor string                  `json:"next_cursor"`
}

// ListChatConversations returns this profile's conversations, most recent
// first.
func (a *App) ListChatConversations(cursor string, limit int) string {
	if a.chatSup == nil {
		return `{"items":[]}`
	}
	args := []string{"chat", "history", "list", "--limit", strconv.Itoa(clampChatLimit(limit))}
	if cursor != "" {
		args = append(args, "--cursor", cursor)
	}
	var page chatConversationPage
	if err := a.chatSup.cli(a.getActiveProfileID(), &page, args...); err != nil {
		return a.chatBindingError(err)
	}
	items := make([]ai.Conversation, 0, len(page.Items))
	for _, r := range page.Items {
		items = append(items, r.Conversation())
	}
	b, _ := json.Marshal(map[string]any{"items": items, "nextCursor": page.NextCursor})
	return string(b)
}

type chatTurnPage struct {
	Items      []ai.TurnRecord `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

// GetChatTurns returns one conversation's turns, most recent first. Each
// item carries ownedByThisInstance: true for every turn except one that is
// active AND owned by a different live instance — see isForeignActiveTurn.
func (a *App) GetChatTurns(conversationID, cursor string, limit int) string {
	if a.chatSup == nil {
		return `{"items":[]}`
	}
	args := []string{"chat", "history", "turns", conversationID, "--limit", strconv.Itoa(clampChatLimit(limit))}
	if cursor != "" {
		args = append(args, "--cursor", cursor)
	}
	var page chatTurnPage
	if err := a.chatSup.cli(a.getActiveProfileID(), &page, args...); err != nil {
		if chatCLIExitCode(err) == 2 { // unknown conversation: no turns, as before
			return `{"items":[],"nextCursor":""}`
		}
		return a.chatBindingError(err)
	}
	out := make([]chatTurnListItem, 0, len(page.Items))
	for _, r := range page.Items {
		t := r.Turn()
		out = append(out, chatTurnListItem{Turn: t, OwnedByThisInstance: !isForeignActiveTurn(t, a.chatSup.instanceID)})
	}
	b, _ := json.Marshal(map[string]any{"items": out, "nextCursor": page.NextCursor})
	return string(b)
}

type chatEventPage struct {
	Items            []chatevents.Record `json:"items"`
	LastCommittedSeq int64               `json:"last_committed_seq"`
	HasMore          bool                `json:"has_more"`
}

// GetChatEvents returns one turn's events after afterSeq, ascending.
func (a *App) GetChatEvents(conversationID, turnID string, afterSeq int64, limit int) string {
	if a.chatSup == nil {
		return `{"items":[]}`
	}
	var page chatEventPage
	err := a.chatSup.cli(a.getActiveProfileID(), &page, "chat", "history", "events", conversationID, turnID,
		"--after-seq", strconv.FormatInt(afterSeq, 10), "--limit", strconv.Itoa(clampChatLimit(limit)))
	if err != nil {
		if chatCLIExitCode(err) == 2 { // unknown turn: no events, as before
			return `{"items":[],"lastCommittedSeq":0,"hasMore":false}`
		}
		return a.chatBindingError(err)
	}
	events := make([]chatevents.Event, 0, len(page.Items))
	for _, r := range page.Items {
		events = append(events, r.Event())
	}
	b, _ := json.Marshal(map[string]any{
		"items":            events,
		"lastCommittedSeq": page.LastCommittedSeq,
		"hasMore":          page.HasMore,
	})
	return string(b)
}

// DeleteChatConversation removes a conversation and its turns/events —
// refused while a turn is active.
func (a *App) DeleteChatConversation(conversationID string) string {
	if a.chatSup == nil {
		return a.chatBindingError(fmt.Errorf("chat supervisor not initialized"))
	}
	if err := a.chatSup.cli(a.getActiveProfileID(), nil, "chat", "history", "delete", conversationID); err != nil {
		return a.chatBindingError(err)
	}
	return `{"ok":true}`
}

func clampChatLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

// wailsChatEmitter is the real emitter used outside tests: emits the
// committed event as-is on the "chat:event" channel the frontend
// subscribes to.
func wailsChatEmitter(ctx context.Context) chatEventEmitter {
	return func(ev chatevents.Event) {
		runtime.EventsEmit(ctx, "chat:event", ev)
	}
}

// initChatSupervisor wires a.chatSup (called from startup()) and runs the
// orphaned-turn sweep in the background, so a slow CLI never delays
// startup. The supervisor reaches the history only through the CLI.
func (a *App) initChatSupervisor() {
	a.chatSup = newChatSupervisor(defaultChatProcessLauncher, wailsChatEmitter(a.ctx), findMonoAgentCLI)
	sup := a.chatSup
	go func() {
		for _, e := range sup.reconcileOrphanedTurns() {
			a.emitLog("SYSTEM", "WARN", fmt.Sprintf("chat turn reconciliation: %v", e))
		}
	}()
}
