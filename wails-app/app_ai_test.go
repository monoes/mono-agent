package main

import (
	"encoding/json"
	"testing"
)

// Regression test for a wire-shape mismatch: a tool_result NDJSON frame from
// monomind's StdioToolBridge always nests its payload as
// {"result":{"text":"..."}}, never a top-level "text" key (that key is only
// ever populated on "assistant" frames). StreamAgentChat's parser used to
// read ev.Text for a tool_result too, which always came back empty, so the
// GUI's tool-call card showed a blank result even when the tool call
// actually succeeded.
func TestAgentStreamEvent_ToolResultTextIsNested(t *testing.T) {
	line := `{"v":1,"type":"tool_result","id":"tc_1","ok":true,"result":{"text":"{\"filename\":\"x.md\"}"}}`
	var ev agentStreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Type != "tool_result" {
		t.Fatalf("Type = %q, want tool_result", ev.Type)
	}
	if ev.ID != "tc_1" {
		t.Errorf("ID = %q, want tc_1", ev.ID)
	}
	if ev.Result.Text != `{"filename":"x.md"}` {
		t.Errorf("Result.Text = %q, want the nested result.text content", ev.Result.Text)
	}
	if ev.Text != "" {
		t.Errorf("Text = %q, want empty -- a tool_result frame never carries a top-level text field", ev.Text)
	}
}

func TestAgentStreamEvent_AssistantTextIsTopLevel(t *testing.T) {
	line := `{"v":1,"type":"assistant","text":"hello there"}`
	var ev agentStreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Text != "hello there" {
		t.Errorf("Text = %q, want %q", ev.Text, "hello there")
	}
}

func TestAgentStreamEvent_ToolCallCarriesNameAndID(t *testing.T) {
	line := `{"v":1,"type":"tool_call","id":"tc_1","name":"save_document","args":{"filename":"x.md"}}`
	var ev agentStreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Name != "save_document" {
		t.Errorf("Name = %q, want save_document", ev.Name)
	}
	if ev.ID != "tc_1" {
		t.Errorf("ID = %q, want tc_1", ev.ID)
	}
}
