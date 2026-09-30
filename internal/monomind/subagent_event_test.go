package monomind

import (
	"encoding/json"
	"testing"
)

// A `subagent` event (monomind#387, protocol §3.2.1) decodes into
// SubagentFields and round-trips.
func TestSubagentEventDecodes(t *testing.T) {
	line := `{"v":1,"type":"subagent","phase":"finished","id":"task_1","tool_use_id":"toolu_task","status":"completed","summary":"Config is loaded by loadConfig().","usage":{"total_tokens":5120,"tool_uses":1,"duration_ms":3050}}`
	var ev Event
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != EventSubagent || ev.Phase != "finished" || ev.ID != "task_1" || ev.ToolUseID != "toolu_task" || ev.Status != "completed" ||
		ev.Summary != "Config is loaded by loadConfig()." || ev.Usage == nil || ev.Usage.DurationMs != 3050 || ev.Usage.TotalTokens != 5120 {
		t.Errorf("event = %+v", ev)
	}
	started := `{"v":1,"type":"subagent","phase":"started","id":"task_1","tool_use_id":"toolu_task","subagent_type":"Explore","description":"Find config loader","prompt":"Find it."}`
	if err := json.Unmarshal([]byte(started), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.SubagentType != "Explore" || ev.Description != "Find config loader" || ev.Prompt != "Find it." || ev.Usage != nil {
		t.Errorf("started = %+v", ev)
	}
	b, _ := json.Marshal(ev)
	var back Event
	if err := json.Unmarshal(b, &back); err != nil || back.SubagentFields.Prompt != "Find it." || back.ToolUseID != "toolu_task" {
		t.Errorf("round trip = %s (%v)", b, err)
	}
}

// A subagent's text is not the agent's answer.
func TestApplyEventToResultSkipsSubagentText(t *testing.T) {
	var res TurnResult
	for _, line := range []string{
		`{"v":1,"type":"assistant","text":"The answer."}`,
		`{"v":1,"type":"assistant","text":"Subagent chatter.","parent_tool_use_id":"toolu_task"}`,
	} {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		ApplyEventToResult(&res, ev)
	}
	if res.ResultText != "The answer." {
		t.Errorf("ResultText = %q", res.ResultText)
	}
}
