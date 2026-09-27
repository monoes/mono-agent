package main

import (
	"fmt"
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

// toolActivityLocked journals one of the agent's own tool calls as the same
// tool.started / tool.completed pair a caller tool produces, marked native.
func (j *turnJournal) toolActivityLocked(ev monomind.Event) {
	switch ev.Phase {
	case "start":
		j.forceFlushLocked()
		args, _ := chatevents.RedactAndBoundFields(ev.Input)
		_ = j.appendLocked(chatevents.EventToolStarted, chatevents.ToolStartedPayload{
			CallID: ev.ID, Name: ev.Name, Arguments: args, Native: true, ParentCallID: ev.ParentToolUseID,
		})
	case "end":
		output, cut, _ := chatevents.BoundText(ev.Output, chatevents.MaxToolPreviewBytes)
		ok := ev.OK
		if ok == nil {
			v := !ev.Denied && !ev.Cancelled
			ok = &v
		}
		_ = j.appendLocked(chatevents.EventToolCompleted, chatevents.ToolCompletedPayload{
			CallID: ev.ID, OK: ok, Result: output, Truncated: cut || ev.OutputTruncated,
			DurationMs: ev.DurationMs, Denied: ev.Denied, Cancelled: ev.Cancelled,
		})
	}
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
		var failed []string
		for _, s := range ev.MCPServers {
			if s.Status != "connected" {
				failed = append(failed, s.Name+" ("+s.Status+")")
			}
		}
		if len(failed) > 0 {
			return "Ready. MCP servers not connected: " + strings.Join(failed, ", ")
		}
		return "Ready"
	}
	return ""
}

// backgroundMessage describes processes a turn left running.
func backgroundMessage(pids []int) string {
	ids := make([]string, len(pids))
	for i, p := range pids {
		ids[i] = strconv.Itoa(p)
	}
	noun := "processes"
	if len(pids) == 1 {
		noun = "process"
	}
	return fmt.Sprintf("%d background %s still running: %s", len(pids), noun, strings.Join(ids, ", "))
}
