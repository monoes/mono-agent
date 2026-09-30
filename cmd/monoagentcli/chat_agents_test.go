package main

import (
	"encoding/json"
	"testing"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

func rec(typ chatevents.EventType, payload any) chatevents.Record {
	b, _ := json.Marshal(payload)
	return chatevents.Record{Type: typ, Payload: b}
}

func TestAgentTranscriptsSplitATurnByAgent(t *testing.T) {
	items := agentTranscripts([]chatevents.Record{
		rec(chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: "part-1", Text: "I'll split it."}),
		rec(chatevents.EventAgentSpawned, chatevents.AgentSpawnedPayload{AgentID: "w1", Role: "Coder", Runtime: "claude", Model: "opus"}),
		rec(chatevents.EventAgentMessage, chatevents.AgentMessagePayload{AgentID: "w1", Direction: "brief", From: "lead", To: "w1", Text: "fix it"}),
		rec(chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{AgentID: "w1", PartID: "w1:p1", Text: "Look"}),
		rec(chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{AgentID: "w1", PartID: "w1:p1", Text: "ing."}),
		rec(chatevents.EventToolStarted, chatevents.ToolStartedPayload{AgentID: "w1", CallID: "w1:t1", Name: "Edit"}),
		rec(chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{AgentID: "w1", PartID: "w1:p2", Text: "Fixed."}),
		rec(chatevents.EventAgentReassigned, chatevents.AgentReassignedPayload{AgentID: "w1", ToRuntime: "codex", ToModel: "gpt-5"}),
		rec(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{AgentID: "w1", To: "done"}),
		rec(chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: "part-2", Text: "Done."}),
	})
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	lead, w := items[0], items[1]
	if lead.AgentID != "lead" || lead.Text != "I'll split it.\n\nDone." {
		t.Errorf("lead = %+v", lead)
	}
	if w.Text != "Looking.\n\nFixed." || w.Model != "gpt-5" || w.Runtime != "codex" || w.Status != "done" || w.Tools != 1 || len(w.Messages) != 1 {
		t.Errorf("w1 = %+v", w)
	}
}
