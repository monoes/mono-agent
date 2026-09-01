package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// AI Providers
// ─────────────────────────────────────────────────────────────────────────────

func (a *App) ListAIProviders() string {
	if a.aiStore == nil {
		return "[]"
	}
	providers, err := a.aiStore.ListProviders(a.getActiveProfileID())
	if err != nil {
		return aiError(err)
	}
	b, _ := json.Marshal(providers)
	return string(b)
}

func (a *App) SaveAIProvider(providerJSON string) string {
	if a.aiStore == nil {
		return aiError(fmt.Errorf("ai store not initialized"))
	}
	var p ai.AIProvider
	if err := json.Unmarshal([]byte(providerJSON), &p); err != nil {
		return aiError(err)
	}
	if p.ID == "" {
		p.ID = newUUID()
	} else if _, err := a.aiStore.GetProvider(p.ID, a.getActiveProfileID()); err != nil {
		return aiError(fmt.Errorf("provider %s not found", p.ID))
	}
	p.ProfileID = a.getActiveProfileID()
	if err := a.aiStore.SaveProvider(p); err != nil {
		return aiError(err)
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func (a *App) DeleteAIProvider(id string) string {
	if a.aiStore == nil {
		return aiError(fmt.Errorf("ai store not initialized"))
	}
	if err := a.aiStore.DeleteProvider(id, a.getActiveProfileID()); err != nil {
		return aiError(err)
	}
	return `{"ok":true}`
}

func (a *App) TestAIProvider(id string) string {
	if a.aiStore == nil {
		return aiError(fmt.Errorf("ai store not initialized"))
	}
	p, err := a.aiStore.GetProvider(id, a.getActiveProfileID())
	if err != nil {
		return aiError(err)
	}
	client, err := ai.NewClient(p)
	if err != nil {
		return aiError(err)
	}
	model := p.DefaultModel
	if model == "" {
		def, ok := ai.GetProviderDef(p.ProviderID)
		if ok && len(def.Models) > 0 {
			model = def.Models[0].ID
		} else {
			model = "gpt-4o-mini"
		}
	}
	_, err = client.Complete(context.Background(), ai.CompletionRequest{
		Model:     model,
		Messages:  []ai.Message{{Role: ai.RoleUser, Content: "Say ok"}},
		MaxTokens: 5,
	})
	status := "active"
	if err != nil {
		status = "error"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_ = a.aiStore.UpdateProviderStatus(id, status, now, a.getActiveProfileID())
	if err != nil {
		return fmt.Sprintf(`{"status":"error","error":%q}`, err.Error())
	}
	return `{"status":"active"}`
}

func (a *App) GetAIModels(providerID string) string {
	def, ok := ai.GetProviderDef(providerID)
	if !ok {
		return "[]"
	}
	b, _ := json.Marshal(def.Models)
	return string(b)
}

func (a *App) GetAIRegistry() string {
	b, _ := json.Marshal(ai.ProviderRegistry)
	return string(b)
}

func aiError(err error) string {
	return fmt.Sprintf(`{"error":%q}`, err.Error())
}

// ─────────────────────────────────────────────────────────────────────────────
// AI Chat
// ─────────────────────────────────────────────────────────────────────────────

func (a *App) StreamAIChat(workflowID, message, providerID, model string) string {
	if a.chatService == nil {
		return aiError(fmt.Errorf("chat service not initialized"))
	}
	a.chatService.SetProfileID(a.getActiveProfileID())
	// Cancellable, parented to the app context so shutdown stops the stream.
	ctx, cancel := context.WithCancel(a.ctx)
	h := &cancelHandle{cancel: cancel}
	// A new stream for the same workflow supersedes any in-flight one.
	if prev, loaded := a.chatCancels.Swap(workflowID, h); loaded {
		if ph, ok := prev.(*cancelHandle); ok {
			ph.cancel()
		}
	}
	go func() {
		defer func() {
			a.chatCancels.CompareAndDelete(workflowID, h)
			cancel()
		}()
		err := a.chatService.StreamChat(
			ctx,
			workflowID, message, providerID, model,
			func(chunk ai.StreamChunk) {
				runtime.EventsEmit(a.ctx, "ai:chunk", map[string]interface{}{
					"workflowID": workflowID,
					"content":    chunk.Content,
					"done":       chunk.Done,
				})
			},
			func(name, args, result string) {
				runtime.EventsEmit(a.ctx, "ai:tool", map[string]interface{}{
					"workflowID": workflowID,
					"tool":       name,
					"args":       args,
					"result":     result,
				})
			},
		)
		if err != nil && ctx.Err() == nil {
			// A genuine error (not a user-initiated Stop, which cancels ctx and
			// surfaces as context.Canceled).
			runtime.EventsEmit(a.ctx, "ai:error", map[string]interface{}{
				"workflowID": workflowID,
				"error":      err.Error(),
			})
		} else {
			// Completed normally, or stopped by the user — finalize with a clean
			// done chunk so any partial streamed content is preserved (the
			// frontend commits it on done) instead of showing an error.
			runtime.EventsEmit(a.ctx, "ai:chunk", map[string]interface{}{
				"workflowID": workflowID,
				"content":    "",
				"done":       true,
			})
		}
	}()
	return `{"ok":true}`
}

// StopAIChat cancels an in-flight AI chat stream for the given workflow.
func (a *App) StopAIChat(workflowID string) string {
	if v, ok := a.chatCancels.Load(workflowID); ok {
		if h, ok := v.(*cancelHandle); ok {
			h.cancel()
		}
		return `{"ok":true}`
	}
	return `{"ok":false,"error":"no active stream"}`
}

func (a *App) GetAIChatHistory(workflowID string) string {
	if a.chatService == nil {
		return "[]"
	}
	a.chatService.SetProfileID(a.getActiveProfileID())
	msgs, err := a.chatService.GetHistory(workflowID)
	if err != nil {
		return aiError(err)
	}
	b, _ := json.Marshal(msgs)
	return string(b)
}

func (a *App) ClearAIChatHistory(workflowID string) string {
	if a.chatService == nil {
		return aiError(fmt.Errorf("chat service not initialized"))
	}
	a.chatService.SetProfileID(a.getActiveProfileID())
	if err := a.chatService.ClearHistory(workflowID); err != nil {
		return aiError(err)
	}
	return `{"ok":true}`
}

// ListChatSessions returns a workflow's past resumable chat sessions
// (most-recently-updated first), for the panel's session-history dropdown.
func (a *App) ListChatSessions(workflowID string) string {
	if a.aiStore == nil {
		return "[]"
	}
	sessions, err := a.aiStore.ListChatSessions(workflowID)
	if err != nil {
		return aiError(err)
	}
	b, _ := json.Marshal(sessions)
	return string(b)
}

// GetChatSessionMessages returns one resumable session's full message
// history, for loading a past session back into the panel.
func (a *App) GetChatSessionMessages(workflowID, sessionID string) string {
	if a.aiStore == nil {
		return "[]"
	}
	msgs, err := a.aiStore.GetSessionMessages(workflowID, sessionID)
	if err != nil {
		return aiError(err)
	}
	b, _ := json.Marshal(msgs)
	return string(b)
}

// ─────────────────────────────────────────────────────────────────────────────
// Agent chat (monomind delegation — Agent Exec Protocol)
//
// The in-process provider chat above remains during the parallel-run window;
// these bindings are the doctrine-compliant replacement: the UI is a runner
// of `monoagentcli chat`, whose NDJSON stdout IS the stream contract.
// ─────────────────────────────────────────────────────────────────────────────

// ScanAgentRuntimes detects installed agent runtimes through the monomind
// engine. Returns JSON {v, agents} or {error} with the actionable install
// hint when monomind itself is missing.
func (a *App) ScanAgentRuntimes() string {
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	res, err := monomind.Scan(ctx)
	if err != nil {
		return aiError(err)
	}
	b, _ := json.Marshal(res)
	return string(b)
}

// StreamAgentChat runs one chat turn through the locally installed agent
// runtime. When canvas is true, the turn runs in workflow-builder mode: the
// eight CanvasTools are wired in and the system prompt instructs the model
// to build/edit the given workflow — this is what the Workflows editor
// wants. When canvas is false (the general Agents-page assistant, where
// workflowID is just a chat-history bucket, not a real workflow to build),
// no --canvas flag is passed at all, so the turn is a plain conversation —
// no tool round trips, no "build a workflow to say hi" behavior, and
// dramatically faster (unless monoagentTools is also set — see below).
// Emits the same ai:chunk/ai:tool/ai:error events as the provider chat
// (frontend-compatible), plus agent:session carrying the resumable session
// id. monoagentTools, when true, additionally wires the MonoagentTools
// surface (--tools monoagent) — workflow/vault/people/actions/communications
// access, read/write but NO run execution — composable with canvas mode.
// allowRuns, when true AND monoagentTools is also true, upgrades the flag
// to --tools monoagent,runs so run_workflow/run_action may execute; it
// must only ever come from an explicit, persisted user setting (default
// false) — never from anything a chat turn can influence, and it has no
// effect on its own. resumeSessionID, when non-empty, continues that prior
// agent session (real conversational memory in the runtime itself, not
// just replayed transcript) instead of starting a new one — the frontend
// supplies whatever it last learned from an agent:session event, and
// clears it to start a fresh conversation.
func (a *App) StreamAgentChat(workflowID, message, agentRuntime, model, resumeSessionID string, canvas bool, monoagentTools bool, allowRuns bool) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	args := []string{"--profile", a.getActiveProfileID(), "chat", "--runtime", agentRuntime, "--history-id", workflowID}
	if canvas {
		args = append(args, "--canvas", workflowID)
	}
	if monoagentTools {
		toolsFlag := "monoagent"
		if allowRuns {
			toolsFlag = "monoagent,runs"
		}
		args = append(args, "--tools", toolsFlag)
	}
	if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, message)

	// Logged separately from args: the invocation line omits the raw
	// message (may be long/sensitive) but keeps every flag, so a flag
	// typo/mismatch (e.g. an old CLI binary that doesn't know --tools yet)
	// is immediately visible in the live log instead of only showing up as
	// an opaque "unknown flag" line further down.
	a.emitLog("AI", "INFO", fmt.Sprintf("$ %s %s <%d-char message>",
		cliBin, strings.Join(args[:len(args)-1], " "), len(message)))

	cmd := exec.Command(cliBin, args...)
	setChatProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return aiError(err)
	}
	cmd.Stderr = a.chatLogWriter()
	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		a.emitLog("AI", "ERROR", fmt.Sprintf("chat failed to start: %v", err))
		return aiError(fmt.Errorf("start chat: %w", err))
	}

	// Supersede any in-flight agent chat for this workflow.
	a.runningMu.Lock()
	if prev, ok := a.runningCmds["chat:"+workflowID]; ok {
		killChatProcessGroup(prev)
		delete(a.runningCmds, "chat:"+workflowID)
	}
	a.runningCmds["chat:"+workflowID] = cmd
	a.runningMu.Unlock()

	// isCurrentChatCmd reports whether cmd is still the registered subprocess
	// for workflowID. When a new chat call supersedes an in-flight one (line
	// ~354, killChatProcessGroup), the OLD call's reader goroutine below is
	// never told — it just keeps scanning the killed process's stdout until
	// it EOFs, then unconditionally used to emit its own final
	// {done:true}. Since the frontend keys purely on workflowID (no
	// per-call/generation id), that stale done event could race a NEWER
	// turn's still-in-progress stream and finalize it early — the exact
	// "spinner stops but content is still partial" symptom this guards
	// against. Checked before every emission, not just the final one: a
	// killed process's already-buffered-but-unread stdout bytes could still
	// be scanned after the kill, and none of that stale output should ever
	// reach the frontend once superseded either.
	isCurrentChatCmd := func() bool {
		a.runningMu.Lock()
		defer a.runningMu.Unlock()
		return a.runningCmds["chat:"+workflowID] == cmd
	}

	go func() {
		defer func() {
			a.runningMu.Lock()
			// Only clear the registry entry if we're still the current cmd —
			// a superseded goroutine must not delete a newer cmd's entry.
			if a.runningCmds["chat:"+workflowID] == cmd {
				delete(a.runningCmds, "chat:"+workflowID)
			}
			a.runningMu.Unlock()
		}()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		sawDone := false
		superseded := false
		for sc.Scan() {
			if !superseded && !isCurrentChatCmd() {
				superseded = true
			}
			if superseded {
				// Keep draining to real EOF — cmd.Wait() below still must
				// run (it's the only thing that reaps this process;
				// killChatProcessGroup only signals it, per proc_unix.go/
				// proc_windows.go), and Wait() expects the pipe to have
				// been fully read first. Just stop emitting: none of this
				// killed/replaced process's remaining output belongs to
				// the frontend anymore.
				continue
			}
			line := sc.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			var ev struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				SessionID string `json:"session_id"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				Code      string `json:"code"`
				Message   string `json:"message"`
				Fatal     bool   `json:"fatal"`
			}
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			switch ev.Type {
			case "assistant":
				println("[DEBUGEMIT] ai:chunk wf=" + workflowID + " len=" + fmt.Sprint(len(ev.Text)))
				runtime.EventsEmit(a.ctx, "ai:chunk", map[string]interface{}{
					"workflowID": workflowID,
					"content":    ev.Text,
					"done":       false,
				})
			case "session":
				runtime.EventsEmit(a.ctx, "agent:session", map[string]interface{}{
					"workflowID": workflowID,
					"session_id": ev.SessionID,
					"runtime":    agentRuntime,
				})
			case "tool_call":
				runtime.EventsEmit(a.ctx, "ai:tool", map[string]interface{}{
					"workflowID": workflowID,
					"tool":       ev.Name,
					"args":       string(line),
					"result":     "",
					"pending":    true,
				})
			case "tool_result":
				runtime.EventsEmit(a.ctx, "ai:tool", map[string]interface{}{
					"workflowID": workflowID,
					"tool":       ev.Name,
					"result":     ev.Text,
					"call_id":    ev.ID,
				})
			case "error":
				runtime.EventsEmit(a.ctx, "ai:error", map[string]interface{}{
					"workflowID": workflowID,
					"error":      ev.Message,
					"code":       ev.Code,
					"fatal":      ev.Fatal,
				})
			case "done":
				sawDone = true
			}
		}
		if !superseded && !isCurrentChatCmd() {
			superseded = true // supersession landed right as stdout hit real EOF
		}
		waitErr := cmd.Wait() // always reap, superseded or not
		if superseded {
			return // don't emit a stale done for a killed/replaced process
		}
		elapsed := time.Since(startedAt)
		runtime.EventsEmit(a.ctx, "ai:chunk", map[string]interface{}{
			"workflowID": workflowID,
			"content":    "",
			"done":       true,
		})
		switch {
		case waitErr != nil:
			a.emitLog("AI", "ERROR", fmt.Sprintf("chat exited after %s: %v", elapsed.Round(time.Millisecond), waitErr))
		case !sawDone:
			a.emitLog("AI", "WARN", fmt.Sprintf("agent chat stream ended without a done event after %s", elapsed.Round(time.Millisecond)))
		default:
			a.emitLog("AI", "INFO", fmt.Sprintf("chat finished in %s", elapsed.Round(time.Millisecond)))
		}
	}()
	return `{"ok":true}`
}

// StopAgentChat kills the in-flight agent chat subprocess tree for a workflow.
func (a *App) StopAgentChat(workflowID string) string {
	a.runningMu.Lock()
	cmd, ok := a.runningCmds["chat:"+workflowID]
	if ok {
		delete(a.runningCmds, "chat:"+workflowID)
	}
	a.runningMu.Unlock()
	if !ok {
		return `{"ok":false,"error":"no active agent chat"}`
	}
	killChatProcessGroup(cmd)
	return `{"ok":true}`
}

// chatLogWriter sinks the chat CLI's stderr into the UI log pane.
func (a *App) chatLogWriter() *chatLogPipe {
	return &chatLogPipe{app: a}
}

type chatLogPipe struct {
	app *App
	buf []byte
}

func (p *chatLogPipe) Write(b []byte) (int, error) {
	p.buf = append(p.buf, b...)
	for {
		i := indexOfByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(p.buf[:i]))
		p.buf = p.buf[i+1:]
		if line != "" {
			p.app.emitLog("AI", "INFO", line)
		}
	}
	return len(b), nil
}

func indexOfByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
