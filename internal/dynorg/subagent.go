package dynorg

import (
	"strconv"
	"sync"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// NativeAgentID is the org stage's id for the native subagent that the
// tool call callID (as journaled) started: the same id the stage gives it
// from the call itself, so either source updates one node.
func NativeAgentID(callID string) string { return "native:" + callID }

// maxSubagentText caps one subagent message journaled as its text.
const maxSubagentText = 16 * 1024

// Subagents journals an agent's native subagents from monomind's
// `subagent` events and their text (monomind#387, protocol §3.2.1): an
// agent.spawned (agentType "native") on started, agent.status on
// progress, their own text as assistant.delta, and agent.message (result)
// plus agent.finished on finished. The stage then stops inferring them
// from the Task call. Older monomind sends none of these and the stage
// keeps inferring. The zero value is ready to use.
type Subagents struct {
	mu    sync.Mutex
	open  map[string]bool // tool_use_id -> started and not finished
	parts map[string]int  // native agent id -> text parts so far
}

// Handle journals ev when it is a subagent event or a subagent's text and
// reports whether it was one; the caller then leaves it alone (a
// subagent's text is not the agent's). callPrefix is what the caller puts
// before a tool call id to journal it ("w1:" for a worker, "" for the
// lead), and owner the agent whose call started the subagent ("" = the
// lead).
func (s *Subagents) Handle(emit Emitter, ev monomind.Event, callPrefix, owner string) bool {
	switch {
	case ev.Type == monomind.EventSubagent && ev.ToolUseID != "":
	case ev.Type == monomind.EventAssistant && ev.ParentToolUseID != "":
		s.text(emit, ev, callPrefix)
		return true
	default:
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open == nil {
		s.open, s.parts = map[string]bool{}, map[string]int{}
	}
	id := NativeAgentID(callPrefix + ev.ToolUseID)
	switch ev.Phase {
	case "started":
		s.open[ev.ToolUseID] = true
		role := ev.SubagentType
		if role == "" {
			role = ev.Description
		}
		brief := ev.Prompt
		if brief == "" {
			brief = ev.Description
		}
		emit.Emit(chatevents.EventAgentSpawned, chatevents.AgentSpawnedPayload{
			AgentID: id, ParentID: owner, Role: role, AgentType: "native", Brief: boundText(brief, maxReport),
		})
		emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{AgentID: id, To: chatevents.AgentWorking})
	case "progress":
		if !s.open[ev.ToolUseID] {
			return true
		}
		detail := ev.Summary
		if detail == "" && ev.LastTool != "" {
			detail = "using " + ev.LastTool
		}
		emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{
			AgentID: id, From: chatevents.AgentWorking, To: chatevents.AgentWorking, Detail: boundText(detail, 300),
		})
	case "finished":
		if !s.open[ev.ToolUseID] {
			return true
		}
		delete(s.open, ev.ToolUseID)
		if ev.Summary != "" {
			to := owner
			if to == "" {
				to = LeadAgentID
			}
			bounded, cut, _ := chatevents.BoundText(ev.Summary, maxReport)
			emit.Emit(chatevents.EventAgentMessage, chatevents.AgentMessagePayload{
				AgentID: id, Direction: "result", From: id, To: to, Text: bounded, Truncated: cut,
			})
		}
		fin := chatevents.AgentFinishedPayload{AgentID: id, Outcome: subagentOutcome(ev.Status), Summary: boundText(ev.Summary, 600)}
		if ev.Usage != nil {
			fin.DurationMs = ev.Usage.DurationMs
		}
		emit.Emit(chatevents.EventAgentFinished, fin)
	}
	return true
}

// text journals a subagent's message as its own text part. monomind sends
// one whole message per subagent model turn, so each is a part.
func (s *Subagents) text(emit Emitter, ev monomind.Event, callPrefix string) {
	if ev.Text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.parts == nil {
		s.open, s.parts = map[string]bool{}, map[string]int{}
	}
	id := NativeAgentID(callPrefix + ev.ParentToolUseID)
	s.parts[id]++
	emit.Emit(chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{
		AgentID: id, PartID: id + ":p" + strconv.Itoa(s.parts[id]), Text: boundText(ev.Text, maxSubagentText),
	})
}

// subagentOutcome maps a finished subagent's status to agent.finished's.
func subagentOutcome(status string) string {
	switch status {
	case "completed", "":
		return chatevents.AgentDone
	case "stopped":
		return chatevents.AgentCancelled
	}
	return chatevents.AgentFailed
}
