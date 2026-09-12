package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/monoes/mono-agent/internal/ai"
	aichat "github.com/monoes/mono-agent/internal/ai/chat"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// Chat supervisor (docs/mastermind/plans/2026-09-11-interactive-agent-chat.md)
//
// This is the SOLE GUI supervisor for the new conversation/turn/event
// architecture: it owns admission, the per-turn registry, event sequencing,
// write-before-emit ordering, process lifecycle, and finalization for both
// the agent (monoagentcli subprocess) and provider (in-process ChatService)
// backends. app_ai.go's StreamAgentChat/StopAgentChat/StreamAIChat and their
// raw ai:chunk/ai:tool/ai:error events remain as separate, untouched
// compatibility bindings — the new UI consumes only the "chat:event" stream
// this file emits, via the bindings at the bottom of this file.
//
// Known, deliberate scope limits (stated here rather than silently assumed):
//   - Startup reconciliation (reconcileOrphanedTurns) marks every turn left
//     "active" in the store as interrupted, unconditionally. It cannot tell
//     a genuinely-dead previous instance apart from a second live instance
//     racing this one at the same instant (no heartbeat/liveness signal is
//     implemented) — for a single-user desktop app sharing one local
//     SQLite file, two truly-concurrent instances is an edge case, not the
//     primary scenario the plan's "two app owners" concern is guarding.
//     Never destructive (only rewrites in-memory-admission-relevant status
//     on rows already left in a non-terminal state), but worth knowing.
//   - The provider backend routes through ChatService.StreamChatScoped,
//     which does not itself support Stop mid-stream (no context-cancellation
//     plumbing inside ai.AIClient.StreamComplete/Complete) — StopChatTurn on
//     a provider turn cancels the turn's own ctx (stopping further event
//     emission and marking it cancelled here) but cannot interrupt a
//     provider HTTP call already in flight underneath. The agent backend's
//     Stop is the fully "kills the real process" implementation the plan
//     describes.
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

// realChatProcess wraps a real *exec.Cmd, reusing the exact process-group
// helpers app_ai.go's StreamAgentChat already uses for the compatibility
// path (proc_unix.go/proc_windows.go).
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
// tests can drive the supervisor's own logic (registry, admission,
// coalescing, finalization) against a fake process without a real
// monoagentcli binary or real tool side effects.
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
// turn. Everything here is only valid while the turn is active; once
// finalized the handle is removed from the registry (history reads go
// through the durable store, not this).
type chatTurnHandle struct {
	profileID      string // captured at admission, immutable for the turn's life
	conversationID string
	turnID         string
	backend        string // "agent" | "provider"
	runtimeID      string

	cancel context.CancelFunc

	mu            sync.Mutex
	stopRequested bool
	proc          chatProcess // nil for provider-backend turns
}

func (h *chatTurnHandle) requestStop() {
	h.mu.Lock()
	already := h.stopRequested
	h.stopRequested = true
	proc := h.proc
	h.mu.Unlock()
	if already {
		return
	}
	h.cancel()
	if proc != nil {
		proc.Kill()
	}
}

func (h *chatTurnHandle) isStopRequested() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopRequested
}

// chatSupervisor owns admission, the per-turn registry, and journal writes.
// Independently constructible (no Wails runtime/App required) so it is
// fully unit-testable — see app_chat_test.go.
type chatSupervisor struct {
	store       *ai.AIStore
	chatService *aichat.ChatService
	launcher    chatProcessLauncher
	emit        chatEventEmitter
	findCLI     func() (string, error)
	// instanceID identifies this running app process, stamped as a turn's
	// OwnerInstanceID — distinguishes "my own turn, resumable/stoppable
	// here" from "another live instance's turn, read-only here" (plan
	// §236). Fresh per process start.
	instanceID string

	mu                   sync.Mutex
	turns                map[chatTurnKey]*chatTurnHandle
	activeByConversation map[string]chatTurnKey // one active turn per conversation
}

func newChatSupervisor(store *ai.AIStore, chatService *aichat.ChatService, launcher chatProcessLauncher, emit chatEventEmitter, findCLI func() (string, error)) *chatSupervisor {
	return &chatSupervisor{
		store:                store,
		chatService:          chatService,
		launcher:             launcher,
		emit:                 emit,
		findCLI:              findCLI,
		instanceID:           uuid.NewString(),
		turns:                make(map[chatTurnKey]*chatTurnHandle),
		activeByConversation: make(map[string]chatTurnKey),
	}
}

// reconcileOrphanedTurns marks every turn left "active" in the store as
// interrupted — see this file's header comment for the known liveness-
// detection limitation. Best-effort: logged, not fatal, on a per-row basis
// via the return value's error slice.
func (sup *chatSupervisor) reconcileOrphanedTurns() []error {
	var errs []error
	rows, err := sup.store.QueryActiveTurns()
	if err != nil {
		return []error{fmt.Errorf("list active turns: %w", err)}
	}
	for _, row := range rows {
		// No live app instance is subscribed to this turn yet at startup —
		// nothing to emit to, so the committed event is intentionally
		// discarded here (unlike finalize, which emits it to the currently
		// running turn's subscriber).
		if _, _, err := sup.store.FinalizeTurn(row.ProfileID, row.ConversationID, row.ID, chatevents.StatusInterrupted, "interrupted at backend startup (previous app instance)", nil, true); err != nil {
			errs = append(errs, fmt.Errorf("reconcile turn %s: %w", row.ID, err))
		}
	}
	return errs
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

// release clears a finalized turn's admission slot. The handle itself is
// dropped from the registry — GetChatTurns/GetChatEvents read the durable
// store for anything past this point, not in-memory state.
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

// stopAll requests stop on every currently-registered turn — called at app
// shutdown so no orphaned subprocess survives the GUI (plan §236: "App
// shutdown cancels its registered turns").
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
}

// ─── Journal write helper ───────────────────────────────────────────────────

// appendAndEmit commits ev to the durable journal, then — only on success —
// emits the identical committed event live. A commit failure is reported
// via onPersistFailure rather than emitting a live-only event silently:
// callers decide how to surface historySaved:false per plan §248.
func (sup *chatSupervisor) appendAndEmit(h *chatTurnHandle, typ chatevents.EventType, payload any) (chatevents.Event, error) {
	ev, err := sup.store.AppendEvent(h.profileID, h.conversationID, h.turnID, typ, payload)
	if err != nil {
		return chatevents.Event{}, err
	}
	if sup.emit != nil {
		sup.emit(ev)
	}
	return ev, nil
}

// ─── Turn message plumbing (single writer goroutine per turn) ──────────────
//
// Both the NDJSON reader and the stale-coalescing-flush ticker send onto one
// channel, consumed by exactly one goroutine per turn (runAgentTurn's inner
// loop). This is what makes "serialize writes per turn" true in practice:
// nothing outside that one loop ever calls appendAndEmit for this turn.

type turnMsg struct {
	ev         *monomind.Event
	tick       bool
	procDone   bool
	waitErr    error
	stderrText string
}

// ─── Agent backend ───────────────────────────────────────────────────────────

// startAgentTurn launches `monoagentcli chat --no-history ...` for an agent
// conversation and drives its NDJSON stdout through the single-writer
// pipeline. Returns immediately after the process is confirmed started;
// finalization happens asynchronously.
func (sup *chatSupervisor) startAgentTurn(h *chatTurnHandle, conv ai.Conversation, turnID, prompt string, tools, allowRuns bool) error {
	cliBin, err := sup.findCLI()
	if err != nil {
		return err
	}
	args := []string{"--profile", h.profileID, "chat", "--no-history", "--runtime", conv.RuntimeID, "--history-id", conv.HistoryKey}
	if conv.SessionID != "" {
		args = append(args, "--resume", conv.SessionID)
	}
	if conv.Model != "" {
		args = append(args, "--model", conv.Model)
	}
	if tools {
		toolsFlag := "monoagent"
		if allowRuns {
			toolsFlag = "monoagent,runs"
		}
		args = append(args, "--tools", toolsFlag)
	}
	args = append(args, prompt)

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.runtimeID = conv.RuntimeID

	proc, err := sup.launcher(ctx, cliBin, args)
	if err != nil {
		cancel()
		return err
	}
	h.mu.Lock()
	h.proc = proc
	h.mu.Unlock()

	if _, err := sup.appendAndEmit(h, chatevents.EventTurnStarted, chatevents.TurnStartedPayload{
		Backend: "agent", Runtime: conv.RuntimeID, Model: conv.Model, Text: prompt,
	}); err != nil {
		proc.Kill()
		cancel()
		// CreateTurn already committed this turn as "active" — without
		// finalizing it here, it never leaves that state until a restart's
		// reconcileOrphanedTurns catches it, and DeleteConversation refuses
		// the conversation in the meantime.
		sup.finalize(h, &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: err.Error()}})
		return err
	}

	go sup.runAgentTurn(h, proc)
	return nil
}

func (sup *chatSupervisor) runAgentTurn(h *chatTurnHandle, proc chatProcess) {
	msgs := make(chan turnMsg, 256)
	var stderrBuf strings.Builder
	var stderrMu sync.Mutex
	// stderrDone is closed once the stderr-draining goroutine below returns,
	// by any path. The wait goroutine joins on it before reading stderrBuf:
	// proc.Wait() returning is not a signal that goroutine has finished
	// draining the pipe, only that stderrBuf is safe to *access* under
	// stderrMu — not that its contents are complete. Deliberately joined
	// AFTER proc.Wait() rather than before: exec.Cmd's StderrPipe is only
	// force-closed (unblocking a stuck Read) once Wait's closeAfterWait
	// runs, so joining first would risk hanging forever against a real
	// process whose stderr fd stays open past our own exit (e.g. a
	// process-group child that inherited fd 2).
	stderrDone := make(chan struct{})

	go func() {
		defer close(stderrDone) // first, so a panic in Read still unblocks the wait goroutine
		stderr := proc.Stderr() // read once, outside the loop: some
		// implementations (e.g. test fakes) return a fresh reader on every
		// call rather than the same underlying stream, which would never
		// reach EOF if re-fetched on each iteration.
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				stderrMu.Lock()
				stderrBuf.Write(buf[:n])
				stderrMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		sc := bufio.NewScanner(proc.Stdout())
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var ev monomind.Event
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			evCopy := ev
			msgs <- turnMsg{ev: &evCopy}
		}
		waitErr := proc.Wait()
		<-stderrDone
		stderrMu.Lock()
		stderrText := stderrBuf.String()
		stderrMu.Unlock()
		msgs <- turnMsg{procDone: true, waitErr: waitErr, stderrText: stderrText}
		close(msgs)
	}()

	sup.writeTurnLoop(h, msgs)
}

// writeTurnLoop is the single writer goroutine for one turn: the only code
// path that calls appendAndEmit for this (conversationID, turnID), fed by
// both the NDJSON reader and stale-flush ticks over one channel.
func (sup *chatSupervisor) writeTurnLoop(h *chatTurnHandle, msgs chan turnMsg) {
	coalescer := chatevents.NewTextCoalescer()
	res := &monomind.TurnResult{}
	partSeq := 0
	currentPartID := ""
	nextPart := func() string { partSeq++; return "part-" + strconv.Itoa(partSeq) }
	sawAnyEvent := false

	commit := func(partID, text string) {
		if text == "" {
			return
		}
		sup.appendAndEmit(h, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: partID, Text: text})
	}
	armTick := func() {
		time.AfterFunc(chatevents.CoalesceWindow+10*time.Millisecond, func() {
			defer func() { recover() }() // msgs may already be closed if the turn finished
			msgs <- turnMsg{tick: true}
		})
	}
	emitUsageIfAny := func(source string) {
		if !res.HasInputTokens && !res.HasOutputTokens && !res.HasCostUSD {
			return
		}
		p := chatevents.UsageUpdatedPayload{Source: source}
		if res.HasInputTokens {
			v := res.InputTokens
			p.InputTokens = &v
		}
		if res.HasOutputTokens {
			v := res.OutputTokens
			p.OutputTokens = &v
		}
		if res.HasCostUSD {
			v := res.CostUSD
			p.CostUSD = &v
		}
		sup.appendAndEmit(h, chatevents.EventUsageUpdated, p)
	}

	for msg := range msgs {
		if msg.tick {
			if partID, text, ok := coalescer.Flush(time.Now()); ok {
				commit(partID, text)
				currentPartID = ""
			}
			continue
		}
		if msg.procDone {
			if partID, text, ok := coalescer.ForceFlush(); ok {
				commit(partID, text)
			}
			sup.finalizeAgentTurn(h, res, sawAnyEvent, msg.waitErr, msg.stderrText)
			return
		}

		ev := *msg.ev
		sawAnyEvent = true
		monomind.ApplyEventToResult(res, ev)

		switch ev.Type {
		case monomind.EventSession:
			if ev.SessionID != "" {
				_ = sup.store.BindConversationSession(h.conversationID, h.profileID, h.runtimeID, ev.SessionID)
				sup.appendAndEmit(h, chatevents.EventSessionBound, chatevents.SessionBoundPayload{Runtime: h.runtimeID, SessionID: ev.SessionID})
			}
		case monomind.EventAssistant:
			if ev.Text != "" {
				if currentPartID == "" {
					currentPartID = nextPart()
				}
				partID, text, flushed := coalescer.Push(currentPartID, ev.Text, time.Now())
				if flushed {
					commit(partID, text)
					currentPartID = ""
				} else {
					armTick()
				}
			}
		case monomind.EventToolCall:
			if partID, text, ok := coalescer.ForceFlush(); ok {
				commit(partID, text)
				currentPartID = ""
			}
			args := chatevents.RedactAndBoundJSON(ev.Args)
			sup.appendAndEmit(h, chatevents.EventToolStarted, chatevents.ToolStartedPayload{CallID: ev.ID, Name: ev.Name, Arguments: args})
		case monomind.EventToolResult:
			resultText := ""
			if ev.Result != nil {
				resultText = ev.Result.Text
			}
			bounded, _, _ := chatevents.BoundText(resultText, chatevents.MaxToolPreviewBytes)
			sup.appendAndEmit(h, chatevents.EventToolCompleted, chatevents.ToolCompletedPayload{CallID: ev.ID, OK: ev.OK, Result: bounded})
		case monomind.EventUsage:
			emitUsageIfAny("usage")
		case monomind.EventResult:
			emitUsageIfAny("result")
		case monomind.EventError:
			if !ev.Fatal {
				if partID, text, ok := coalescer.ForceFlush(); ok {
					commit(partID, text)
					currentPartID = ""
				}
				boundedMsg, _, _ := chatevents.BoundText(ev.ErrMessage, chatevents.MaxToolPreviewBytes)
				sup.appendAndEmit(h, chatevents.EventNotice, chatevents.NoticePayload{Code: ev.Code, Message: boundedMsg, Severity: chatevents.SeverityWarning})
			}
		}
	}
}

// finalizeAgentTurn computes the terminal status and commits turn.finished
// exactly once. A --no-history launch failure (an older monoagentcli that
// doesn't recognize a required flag) shows up as zero events ever parsed
// plus "unknown flag" on stderr — indistinguishable from a hung/crashed
// process otherwise, so it is detected explicitly and reported as a clear,
// distinct failure rather than the generic "interrupted" a bare !SawDone
// would otherwise produce (plan §161: never silently retry as fallback).
func (sup *chatSupervisor) finalizeAgentTurn(h *chatTurnHandle, res *monomind.TurnResult, sawAnyEvent bool, waitErr error, stderrText string) {
	if !sawAnyEvent && strings.Contains(stderrText, "unknown flag") {
		sup.appendAndEmit(h, chatevents.EventNotice, chatevents.NoticePayload{
			Code:     "cli-flag-unsupported",
			Message:  "The installed monoagentcli does not recognize a flag this app requires (--no-history). Update monoagentcli to match this app's version.",
			Severity: chatevents.SeverityError,
		})
		res.Err = &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: "monoagentcli rejected a required flag: " + firstLine(stderrText)}
	} else if !sawAnyEvent && waitErr != nil && res.Err == nil {
		res.Err = &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: "monoagentcli exited before producing any output: " + waitErr.Error()}
	}
	sup.finalize(h, res)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// finalize computes the terminal status/reason, commits turn.finished
// exactly once (FinalizeTurn's compare-and-set makes a second call here a
// harmless no-op), and releases the conversation's admission slot.
func (sup *chatSupervisor) finalize(h *chatTurnHandle, res *monomind.TurnResult) {
	status, reason := chatevents.ComputeTurnStatus(h.isStopRequested(), res)
	var exitCode *int
	if res != nil {
		v := res.ExitCode
		exitCode = &v
	}
	ev, alreadyFinalized, err := sup.store.FinalizeTurn(h.profileID, h.conversationID, h.turnID, status, reason, exitCode, true)
	switch {
	case err != nil:
		// Persistence failed mid/post-turn: retain the live transcript
		// already emitted, but tell the UI history could not be saved for
		// this turn's terminal state (plan §248) instead of hanging. This
		// event is never committed (the store write is exactly what just
		// failed) — live-only, by construction.
		liveEv, buildErr := chatevents.New(h.profileID, h.conversationID, h.turnID, chatevents.MaxSafeSeq, time.Now(), chatevents.EventTurnFinished, chatevents.TurnFinishedPayload{
			Status: status, Reason: reason, ExitCode: exitCode, HistorySaved: false,
		})
		if buildErr == nil && sup.emit != nil {
			sup.emit(liveEv)
		}
	case alreadyFinalized:
		// Another path (e.g. a racing Stop) already finalized this turn —
		// exactly-once by design; ev is the zero value (nothing was
		// written this call), so there is nothing further to emit.
	default:
		if sup.emit != nil {
			sup.emit(ev)
		}
	}
	sup.release(h.conversationID, h.turnID)
}

// ─── Provider backend ────────────────────────────────────────────────────────

// startProviderTurn runs one provider-backend turn in-process via
// ChatService.StreamChatScoped, translating its callbacks into the same
// append-then-emit pipeline the agent backend uses. Runs synchronously in
// its own goroutine (there is no subprocess to supervise), and — like the
// agent path — only this one goroutine ever calls appendAndEmit for this
// turn, satisfying the same "serialize writes per turn" requirement.
func (sup *chatSupervisor) startProviderTurn(h *chatTurnHandle, conv ai.Conversation, prompt string) error {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	if _, err := sup.appendAndEmit(h, chatevents.EventTurnStarted, chatevents.TurnStartedPayload{
		Backend: "provider", Provider: conv.ProviderID, Model: conv.Model, Text: prompt,
	}); err != nil {
		cancel()
		// Same reasoning as startAgentTurn's identical check: without this,
		// CreateTurn's "active" row never gets finalized.
		sup.finalize(h, &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: err.Error()}})
		return err
	}

	go func() {
		res := &monomind.TurnResult{SawDone: true} // a provider call that returns is inherently "terminal evidence seen"
		coalescer := chatevents.NewTextCoalescer()
		partSeq := 0
		currentPartID := nextProviderPart(&partSeq)

		onChunk := func(chunk ai.StreamChunk) {
			if chunk.Content == "" {
				return
			}
			partID, text, flushed := coalescer.Push(currentPartID, chunk.Content, time.Now())
			if flushed {
				sup.appendAndEmit(h, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: partID, Text: text})
				currentPartID = nextProviderPart(&partSeq)
			}
		}
		// onToolStart fires before the tool actually executes, giving a true
		// start timestamp — service.go's tool loop now calls this
		// separately from onToolCall below instead of synthesizing both
		// events back-to-back after execution already finished (the root
		// cause of tool cards always showing ~0.0s elapsed time). The
		// pending-text flush belongs here, not in onToolCall: it closes out
		// any assistant text that preceded this call so ordering is
		// preserved (text, then tool.started, then — later — tool.completed).
		onToolStart := func(callID, name, args string) {
			if partID, text, ok := coalescer.ForceFlush(); ok {
				sup.appendAndEmit(h, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: partID, Text: text})
				currentPartID = nextProviderPart(&partSeq)
			}
			sup.appendAndEmit(h, chatevents.EventToolStarted, chatevents.ToolStartedPayload{CallID: callID, Name: name, Arguments: chatevents.RedactAndBoundJSON(json.RawMessage(args))})
		}
		// onToolCall fires once execution finishes. ok is now derived from
		// the explicit error service.go's tool loop reports, not
		// hardcoded true — the root cause of tool failures always
		// rendering as success. ok is declared fresh inside this closure
		// body on every call, so &ok never aliases a previous call's value.
		onToolCall := func(callID, name, args, result string, toolErr error) {
			bounded, _, _ := chatevents.BoundText(result, chatevents.MaxToolPreviewBytes)
			ok := toolErr == nil
			sup.appendAndEmit(h, chatevents.EventToolCompleted, chatevents.ToolCompletedPayload{CallID: callID, OK: &ok, Result: bounded})
		}

		err := sup.chatService.StreamChatScoped(ctx, h.profileID, conv.HistoryKey, prompt, conv.ProviderID, conv.Model, onChunk, onToolStart, onToolCall)
		if partID, text, ok := coalescer.ForceFlush(); ok {
			sup.appendAndEmit(h, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: partID, Text: text})
		}
		if err != nil && ctx.Err() == nil {
			res.Err = &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: err.Error()}
		}
		sup.finalize(h, res)
	}()
	return nil
}

func nextProviderPart(seq *int) string {
	*seq++
	return "part-" + strconv.Itoa(*seq)
}

// ─── Wails bindings ─────────────────────────────────────────────────────────
//
// New contracts per the plan (§175) — kept alongside, not replacing, the
// existing StreamAIChat/StreamAgentChat compatibility bindings. Responses
// follow the existing JSON-string convention: {"error":...} on failure,
// otherwise a typed payload.

func (a *App) chatBindingError(err error) string { return aiError(err) }

// isForeignActiveTurn reports whether t is an active turn owned by a
// DIFFERENT, identified live instance than instanceID. This is the single
// condition — per the interactive-agent-chat followups' cross-instance-
// ownership contract — under which another live app instance's turn (one
// sharing this same on-disk database) must be treated as read-only here:
// it governs both GetChatTurns' ownedByThisInstance flag and StopChatTurn's
// explicit foreign-turn failure below. A terminal turn is never foreign
// (finished turns are ordinary, fully-owned history, regardless who ran
// them), and neither is an active turn with an empty stored
// OwnerInstanceID — a row predating this check, or any other case where
// there is simply nothing to compare against — which deliberately falls
// back to the prior, already-idempotent "treat as mine/no-op" behavior
// rather than guessing.
func isForeignActiveTurn(t ai.Turn, instanceID string) bool {
	return t.Status == "active" && t.OwnerInstanceID != "" && t.OwnerInstanceID != instanceID
}

// chatTurnListItem is one GetChatTurns response entry: the stored turn plus
// a derived, instance-scoped ownership flag. ai.Turn.OwnerInstanceID itself
// stays json:"-" (never exposed raw) — only whether THIS process still owns
// an active turn here is meaningful to a UI, e.g. to label a foreign-active
// turn read-only.
type chatTurnListItem struct {
	ai.Turn
	OwnedByThisInstance bool `json:"ownedByThisInstance"`
}

// CreateChatConversation creates a new scoped conversation for either
// backend ("agent" or "provider"). workflowID is the tool/ownership context
// ("general"/"draft"/an owned workflow id); an opaque history key is
// generated internally and never exposed to the caller.
func (a *App) CreateChatConversation(backend, workflowID, runtimeID, providerID, model string) string {
	if a.chatSup == nil {
		return a.chatBindingError(fmt.Errorf("chat supervisor not initialized"))
	}
	if backend != "agent" && backend != "provider" {
		return a.chatBindingError(fmt.Errorf("backend must be \"agent\" or \"provider\", got %q", backend))
	}
	conv, err := a.aiStore.CreateConversation(a.getActiveProfileID(), backend, workflowID, runtimeID, providerID, model)
	if err != nil {
		return a.chatBindingError(err)
	}
	b, _ := json.Marshal(conv)
	return string(b)
}

// StartChatTurn admits and starts one turn. Duplicate calls with the same
// turnID against an already-admitted turn return the existing admission
// (idempotent Start); a different turnID while one is active returns busy.
func (a *App) StartChatTurn(conversationID, turnID, message string, tools, allowRuns bool) string {
	if a.chatSup == nil {
		return a.chatBindingError(fmt.Errorf("chat supervisor not initialized"))
	}
	profileID := a.getActiveProfileID()
	conv, err := a.aiStore.GetConversation(conversationID, profileID)
	if err != nil {
		return a.chatBindingError(err)
	}

	h, alreadyActive, err := a.chatSup.admit(conversationID, turnID)
	if err != nil {
		// Every StartChatTurn response carries turnId (plan §189) so a
		// frontend keying pending requests by turn can correlate this
		// refusal back to the call that produced it.
		return fmt.Sprintf(`{"ok":false,"turnId":%q,"status":"busy"}`, turnID)
	}
	if alreadyActive {
		return fmt.Sprintf(`{"ok":true,"turnId":%q,"status":"active"}`, turnID)
	}
	h.profileID = profileID
	h.backend = conv.Backend

	turn, existed, err := a.aiStore.CreateTurn(conversationID, profileID, turnID, a.chatSup.instanceID, message)
	if err != nil {
		a.chatSup.release(conversationID, turnID)
		return a.chatBindingError(fmt.Errorf("admit turn: %w", err))
	}
	if existed && turn.Status != "active" {
		// A previous process's turn with this exact ID already reached a
		// terminal state (e.g. this exact Start was retried after the app
		// restarted) — report it rather than re-running the same prompt.
		a.chatSup.release(conversationID, turnID)
		b, _ := json.Marshal(map[string]any{"ok": true, "turnId": turnID, "status": turn.Status})
		return string(b)
	}

	switch conv.Backend {
	case "agent":
		if err := a.chatSup.startAgentTurn(h, conv, turnID, message, tools, allowRuns); err != nil {
			a.chatSup.release(conversationID, turnID)
			return a.chatBindingError(err)
		}
	case "provider":
		if err := a.chatSup.startProviderTurn(h, conv, message); err != nil {
			a.chatSup.release(conversationID, turnID)
			return a.chatBindingError(err)
		}
	default:
		a.chatSup.release(conversationID, turnID)
		return a.chatBindingError(fmt.Errorf("conversation %s has unknown backend %q", conversationID, conv.Backend))
	}
	return fmt.Sprintf(`{"ok":true,"turnId":%q,"status":"active"}`, turnID)
}

// StopChatTurn requests cancellation of a turn. Idempotent: stopping an
// already-stopped/already-finished turn, or an unknown turn id, remains a
// harmless no-op success. A turn that is currently ACTIVE but owned by a
// DIFFERENT live instance sharing this database is neither of those: this
// instance holds no handle for it (nothing here would actually be
// stopped), so silently returning {"ok":true} would misreport a no-op as a
// completed stop — that specific case reports an explicit failure instead.
// See isForeignActiveTurn.
func (a *App) StopChatTurn(conversationID, turnID string) string {
	if a.chatSup == nil {
		return a.chatBindingError(fmt.Errorf("chat supervisor not initialized"))
	}
	if h := a.chatSup.lookup(conversationID, turnID); h != nil {
		h.requestStop()
		return `{"ok":true}`
	}
	// Not admitted in THIS instance's own registry. Consult the durable
	// store to distinguish "already finished" / "unknown id" (both remain
	// the existing harmless no-op) from "active right now, but owned by
	// another live instance".
	if a.aiStore != nil {
		if turn, err := a.aiStore.GetTurn(turnID, a.getActiveProfileID()); err == nil {
			if isForeignActiveTurn(turn, a.chatSup.instanceID) {
				return `{"ok":false,"error":"this turn is running in another window and can only be stopped there"}`
			}
		}
	}
	return `{"ok":true}`
}

// ListChatConversations returns this profile's conversations, most recent
// first.
func (a *App) ListChatConversations(cursor string, limit int) string {
	if a.aiStore == nil {
		return `{"items":[]}`
	}
	items, next, err := a.aiStore.ListConversations(a.getActiveProfileID(), cursor, clampChatLimit(limit))
	if err != nil {
		return a.chatBindingError(err)
	}
	b, _ := json.Marshal(map[string]any{"items": items, "nextCursor": next})
	return string(b)
}

// GetChatTurns returns one conversation's turns, most recent first. Each
// item carries ownedByThisInstance: true for every turn except one that is
// currently active AND owned by a different live instance sharing this
// database — see isForeignActiveTurn.
func (a *App) GetChatTurns(conversationID, cursor string, limit int) string {
	if a.aiStore == nil {
		return `{"items":[]}`
	}
	items, next, err := a.aiStore.ListTurns(conversationID, a.getActiveProfileID(), cursor, clampChatLimit(limit))
	if err != nil {
		return a.chatBindingError(err)
	}
	instanceID := ""
	if a.chatSup != nil {
		instanceID = a.chatSup.instanceID
	}
	out := make([]chatTurnListItem, len(items))
	for i, t := range items {
		out[i] = chatTurnListItem{Turn: t, OwnedByThisInstance: !isForeignActiveTurn(t, instanceID)}
	}
	b, _ := json.Marshal(map[string]any{"items": out, "nextCursor": next})
	return string(b)
}

// GetChatEvents returns one turn's events after afterSeq, ascending.
func (a *App) GetChatEvents(conversationID, turnID string, afterSeq int64, limit int) string {
	if a.aiStore == nil {
		return `{"items":[]}`
	}
	events, err := a.aiStore.GetEvents(conversationID, turnID, a.getActiveProfileID(), afterSeq, clampChatLimit(limit))
	if err != nil {
		return a.chatBindingError(err)
	}
	turn, turnErr := a.aiStore.GetTurn(turnID, a.getActiveProfileID())
	lastCommittedSeq := int64(0)
	if turnErr == nil {
		lastCommittedSeq = turn.LastCommittedSeq
	}
	b, _ := json.Marshal(map[string]any{
		"items":            events,
		"lastCommittedSeq": lastCommittedSeq,
		"hasMore":          len(events) == clampChatLimit(limit),
	})
	return string(b)
}

// DeleteChatConversation removes a conversation and its turns/events —
// blocked while a turn is active (ai.ErrTurnActive).
func (a *App) DeleteChatConversation(conversationID string) string {
	if a.aiStore == nil {
		return a.chatBindingError(fmt.Errorf("ai store not initialized"))
	}
	if err := a.aiStore.DeleteConversation(conversationID, a.getActiveProfileID()); err != nil {
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
// committed event as-is on the "chat:event" channel the new frontend
// subscribes to exclusively.
func wailsChatEmitter(ctx context.Context) chatEventEmitter {
	return func(ev chatevents.Event) {
		runtime.EventsEmit(ctx, "chat:event", ev)
	}
}

// initChatSupervisor wires a.chatSup once aiStore/chatService are ready
// (called from startup(), after both are constructed). db is accepted for
// symmetry with other init* helpers even though the supervisor itself only
// needs the store/chatService handles.
func (a *App) initChatSupervisor(db *sql.DB) {
	a.chatSup = newChatSupervisor(a.aiStore, a.chatService, defaultChatProcessLauncher, wailsChatEmitter(a.ctx), findMonoAgentCLI)
	if errs := a.chatSup.reconcileOrphanedTurns(); len(errs) > 0 {
		for _, e := range errs {
			a.emitLog("SYSTEM", "WARN", fmt.Sprintf("chat turn reconciliation: %v", e))
		}
	}
}
