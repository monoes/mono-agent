package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// Notice codes a coder turn journals (see the coder mode contract).
const (
	noticeCoderWorkspace  = "coder.workspace"
	noticeCoderStatus     = "coder.status"
	noticeCoderBackground = "coder.background"
)

// nativeCall is what the journal remembers about an open native tool call.
type nativeCall struct {
	name       string
	kind       string
	background bool // a Bash call started with run_in_background
}

// claudeToolKinds maps Claude Code's tool names to tool_activity kinds, for
// a monomind that predates `kind`.
var claudeToolKinds = map[string]string{
	"Bash": "shell", "Edit": "edit", "MultiEdit": "edit", "NotebookEdit": "edit", "Write": "write",
	"Read": "read", "Grep": "search", "Glob": "search", "WebFetch": "web", "WebSearch": "web",
	"Task": "task", "TodoWrite": "todo",
}

// nativeKind is a call's kind: monomind's, else the one its Claude name implies.
func nativeKind(ev monomind.Event) string {
	if ev.Kind != "" {
		return ev.Kind
	}
	return claudeToolKinds[ev.Name]
}

// bashExitCode matches Claude Code's Bash failure output ("Exit code 2").
var bashExitCode = regexp.MustCompile(`(?m)^Exit code (-?\d+)`)

// toolActivityLocked journals one of the agent's own tool calls as the same
// tool.started / tool.completed pair a caller tool produces, marked native.
func (j *turnJournal) toolActivityLocked(ev monomind.Event) {
	switch ev.Phase {
	case "start":
		j.forceFlushLocked()
		args, _ := chatevents.RedactAndBoundFields(ev.Input)
		var input map[string]any
		_ = json.Unmarshal(ev.Input, &input)
		if j.nativeRun == nil {
			j.nativeRun = map[string]nativeCall{}
		}
		kind := nativeKind(ev)
		background, _ := input["run_in_background"].(bool)
		j.nativeRun[ev.ID] = nativeCall{name: ev.Name, kind: kind, background: background}
		_ = j.appendLocked(chatevents.EventToolStarted, chatevents.ToolStartedPayload{
			CallID: ev.ID, Name: ev.Name, Arguments: args, Native: true, ParentCallID: ev.ParentToolUseID,
			FileExisted: j.fileExisted(kind, input), Kind: kind,
		})
	case "end":
		output, cut, _ := chatevents.BoundText(ev.Output, chatevents.MaxToolPreviewBytes)
		ok := ev.OK
		if ok == nil {
			v := !ev.Denied && !ev.Cancelled
			ok = &v
		}
		call, open := j.nativeRun[ev.ID]
		delete(j.nativeRun, ev.ID)
		if !open {
			call = nativeCall{name: ev.Name, kind: nativeKind(ev)}
		}
		var exitCode *int
		if call.kind == "shell" && !call.background && !ev.Denied && !ev.Cancelled {
			switch {
			case ev.HasExitCode:
				code := ev.ExitCode
				exitCode = &code
			case call.name == "Bash":
				exitCode = shellExitCode(*ok, ev.Output)
			}
		}
		_ = j.appendLocked(chatevents.EventToolCompleted, chatevents.ToolCompletedPayload{
			CallID: ev.ID, OK: ok, Result: output, Truncated: cut || ev.OutputTruncated,
			DurationMs: ev.DurationMs, Denied: ev.Denied, Cancelled: ev.Cancelled, ExitCode: exitCode,
		})
	}
}

// closeOpenNativeCallsLocked ends every native call still open as the turn
// finishes, so no tool card is left running. On a runtime that reports
// matched ends, an open call was cut off (a stopped or failed turn; monomind
// closes them itself on a cancel frame, but not on every path). On a
// start-only runtime no call ever gets an end, so each closes with an
// unknown outcome (ok null) instead of as cancelled.
func (j *turnJournal) closeOpenNativeCallsLocked() {
	ids := make([]string, 0, len(j.nativeRun))
	for id := range j.nativeRun {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	startOnly := j.coderRuntime.ToolActivity == "start-only"
	for _, id := range ids {
		p := chatevents.ToolCompletedPayload{CallID: id}
		if !startOnly {
			notOK := false
			p.OK, p.Cancelled = &notOK, true
		}
		_ = j.appendLocked(chatevents.EventToolCompleted, p)
	}
	j.nativeRun = nil
}

// fileExisted reports whether an edit or write call's target (canonical
// file_path; Claude's NotebookEdit uses notebook_path) exists as the call
// starts, i.e. before it runs; nil for other kinds.
func (j *turnJournal) fileExisted(kind string, input map[string]any) *bool {
	if kind != "edit" && kind != "write" {
		return nil
	}
	p, _ := input["file_path"].(string)
	if p == "" {
		p, _ = input["notebook_path"].(string)
	}
	if p == "" {
		return nil
	}
	if !filepath.IsAbs(p) {
		if j.cwd == "" {
			return nil
		}
		p = filepath.Join(j.cwd, p)
	}
	_, err := os.Lstat(p)
	existed := err == nil
	return &existed
}

// shellExitCode is a finished Bash call's exit status: 0 on success, else
// the code Claude Code printed, or nil when it didn't print one.
func shellExitCode(ok bool, output string) *int {
	code := 0
	if !ok {
		m := bashExitCode.FindStringSubmatch(output)
		if m == nil {
			return nil
		}
		code, _ = strconv.Atoi(m[1])
	}
	return &code
}

// notice journals a notice outside the event stream (e.g. the workspace
// line a coder turn starts with).
func (j *turnJournal) notice(code, message string, severity chatevents.NoticeSeverity) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.finished {
		return
	}
	_ = j.appendLocked(chatevents.EventNotice, chatevents.NoticePayload{Code: code, Message: message, Severity: severity})
}

// runtimeNames are the coding CLIs' own names, for status lines.
var runtimeNames = map[string]string{
	"claude": "Claude Code", "codex": "Codex", "opencode": "OpenCode", "antigravity": "Antigravity",
	"kimicode": "Kimi Code", "grok": "Grok", "qwen": "Qwen Code", "copilot": "Copilot", "crush": "Crush", "pi": "Pi",
	"cline": "Cline", "aider": "Aider", "dsh": "DeepSeek Harness", "kilo": "Kilo Code", "freebuff": "Freebuff",
}

// runtimeName is runtime's display name ("" = claude).
func runtimeName(runtime string) string {
	if runtime == "" {
		runtime = monomind.DefaultCoderRuntime
	}
	if n, ok := runtimeNames[runtime]; ok {
		return n
	}
	return runtime
}

// coderStatusMessage renders a status event from runtime's startup as a
// progress line.
func coderStatusMessage(ev monomind.Event, runtime string) string {
	switch ev.Phase {
	case "initializing":
		if n := len(ev.MCPServers); n > 0 {
			return fmt.Sprintf("Starting %s… loading MCP servers (%d)", runtimeName(runtime), n)
		}
		return "Starting " + runtimeName(runtime) + "…"
	case "ready":
		var failed, connecting []string
		for _, s := range ev.MCPServers {
			switch s.Status {
			case "connected":
			case "pending":
				connecting = append(connecting, s.Name)
			default:
				failed = append(failed, s.Name+" ("+s.Status+")")
			}
		}
		msg := "Ready"
		if len(connecting) > 0 {
			msg += ". Still connecting: " + strings.Join(connecting, ", ")
		}
		if len(failed) > 0 {
			msg += ". Not available: " + strings.Join(failed, ", ")
		}
		return msg
	}
	// Other status events (e.g. what a non-claude CLI loads with
	// --settings, or an ignored --effort) carry their own message.
	return ev.ErrMessage
}

// backgroundMessage describes processes a turn left running. They are
// not necessarily the agent's own: the folder's session hooks can start
// daemons too, so each is named by its command.
func backgroundMessage(refs []chatevents.ProcessRef) string {
	items := make([]string, len(refs))
	for i, r := range refs {
		items[i] = strconv.Itoa(r.Pid)
		if r.Command != "" {
			items[i] += " (" + r.Command + ")"
		}
	}
	noun := "processes"
	if len(refs) == 1 {
		noun = "process"
	}
	return fmt.Sprintf("%d %s started during this turn still running: %s", len(refs), noun, strings.Join(items, ", "))
}

// backgroundNotice is the coder.background notice for processes a turn left
// running, with each one's identity recorded for `coder stop-background`.
func backgroundNotice(pids []int) chatevents.NoticePayload {
	refs := make([]chatevents.ProcessRef, len(pids))
	for i, pid := range pids {
		refs[i] = chatevents.ProcessRef{Pid: pid, Identity: processIdentity(pid), Command: shortCommand(pid)}
	}
	return chatevents.NoticePayload{
		Code: noticeCoderBackground, Message: backgroundMessage(refs), Severity: chatevents.SeverityWarning,
		Pids: pids, Processes: refs,
	}
}

// noticeCoderResult is the coder.result notice a turn's result event earns
// when it ran on a model other than the selected one or on a context so
// large that every further call re-reads it (protocol rev 30).
const noticeCoderResult = "coder.result"

// resultNoticesLocked journals unexpected_models and context_warning from a
// result event, each as a warning; a result without them adds nothing.
func (j *turnJournal) resultNoticesLocked(ev monomind.Event) {
	var msgs []string
	if len(ev.UnexpectedModels) > 0 {
		msgs = append(msgs, "This turn also ran on "+strings.Join(ev.UnexpectedModels, ", ")+", not only the selected model.")
	}
	if ev.ContextWarning {
		msgs = append(msgs, fmt.Sprintf("The conversation context reached %d tokens: every further call re-reads it. Consider a new conversation.", ev.PeakContextTokens))
	}
	for _, m := range msgs {
		j.forceFlushLocked()
		bounded, _, _ := chatevents.BoundText(m, chatevents.MaxToolPreviewBytes)
		_ = j.appendLocked(chatevents.EventNotice, chatevents.NoticePayload{Code: noticeCoderResult, Message: bounded, Severity: chatevents.SeverityWarning})
	}
}
