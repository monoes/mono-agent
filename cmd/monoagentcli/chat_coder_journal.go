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
	background bool // a Bash call started with run_in_background
}

// fileTools are the native tools that write a file_path (NotebookEdit's
// target is notebook_path).
var fileTools = map[string]string{"Write": "file_path", "Edit": "file_path", "MultiEdit": "file_path", "NotebookEdit": "notebook_path"}

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
		background, _ := input["run_in_background"].(bool)
		j.nativeRun[ev.ID] = nativeCall{name: ev.Name, background: background}
		_ = j.appendLocked(chatevents.EventToolStarted, chatevents.ToolStartedPayload{
			CallID: ev.ID, Name: ev.Name, Arguments: args, Native: true, ParentCallID: ev.ParentToolUseID,
			FileExisted: j.fileExisted(ev.Name, input),
		})
	case "end":
		output, cut, _ := chatevents.BoundText(ev.Output, chatevents.MaxToolPreviewBytes)
		ok := ev.OK
		if ok == nil {
			v := !ev.Denied && !ev.Cancelled
			ok = &v
		}
		call := j.nativeRun[ev.ID]
		delete(j.nativeRun, ev.ID)
		if call.name == "" {
			call.name = ev.Name
		}
		var exitCode *int
		if call.name == "Bash" && !call.background && !ev.Denied && !ev.Cancelled {
			exitCode = shellExitCode(*ok, ev.Output)
		}
		_ = j.appendLocked(chatevents.EventToolCompleted, chatevents.ToolCompletedPayload{
			CallID: ev.ID, OK: ok, Result: output, Truncated: cut || ev.OutputTruncated,
			DurationMs: ev.DurationMs, Denied: ev.Denied, Cancelled: ev.Cancelled, ExitCode: exitCode,
		})
	}
}

// closeOpenNativeCallsLocked ends every native call still open as the turn
// finishes (a stopped or failed turn), so no tool card is left running.
// monomind closes them itself on a cancel frame, but not on every path.
func (j *turnJournal) closeOpenNativeCallsLocked() {
	ids := make([]string, 0, len(j.nativeRun))
	for id := range j.nativeRun {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		notOK := false
		_ = j.appendLocked(chatevents.EventToolCompleted, chatevents.ToolCompletedPayload{CallID: id, OK: &notOK, Cancelled: true})
	}
	j.nativeRun = nil
}

// fileExisted reports whether a file tool's target exists as the call
// starts, i.e. before it runs; nil for other tools.
func (j *turnJournal) fileExisted(name string, input map[string]any) *bool {
	key, ok := fileTools[name]
	if !ok {
		return nil
	}
	p, _ := input[key].(string)
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

// coderStatusMessage renders a status event as a startup progress line.
func coderStatusMessage(ev monomind.Event) string {
	switch ev.Phase {
	case "initializing":
		if n := len(ev.MCPServers); n > 0 {
			return fmt.Sprintf("Starting Claude Code… loading MCP servers (%d)", n)
		}
		return "Starting Claude Code…"
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
	return ""
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
