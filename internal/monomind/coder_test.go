package monomind

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCoderEventFieldsRoundTrip(t *testing.T) {
	line := `{"v":1,"type":"tool_activity","id":"t1","phase":"end","name":"Bash","ok":false,"output":"boom","output_truncated":true,"duration_ms":9,"denied":true,"parent_tool_use_id":"t0"}`
	var ev Event
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != EventToolActivity || ev.Phase != "end" || ev.Output != "boom" || !ev.OutputTruncated || ev.DurationMs != 9 || !ev.Denied || ev.ParentToolUseID != "t0" || ev.OK == nil || *ev.OK {
		t.Fatalf("decoded = %+v", ev)
	}
	b, _ := json.Marshal(ev)
	var back Event
	json.Unmarshal(b, &back)
	if !reflect.DeepEqual(back.CoderFields, ev.CoderFields) {
		t.Errorf("re-encode lost coder fields: %s", b)
	}

	var st Event
	json.Unmarshal([]byte(`{"v":1,"type":"done","exit_code":0,"background_pids":[1,2]}`), &st)
	if !reflect.DeepEqual(st.BackgroundPids, []int{1, 2}) {
		t.Errorf("background_pids = %v", st.BackgroundPids)
	}
}

func TestMissingCoderCapabilities(t *testing.T) {
	if got := MissingCoderCapabilities(nil); !reflect.DeepEqual(got, CoderCapabilities) {
		t.Errorf("nil set: %v", got)
	}
	set := NewCapabilitySet("9", CapAgentExecFullAccess, CapInitJSON)
	if got := MissingCoderCapabilities(set); !reflect.DeepEqual(got, []string{CapAgentExecSettings, CapAgentExecToolActivity}) {
		t.Errorf("partial set: %v", got)
	}
	if got := MissingCoderCapabilities(NewCapabilitySet("9", CoderCapabilities...)); len(got) != 0 {
		t.Errorf("full set: %v", got)
	}
}

func TestExecFullAccessNeedsCwd(t *testing.T) {
	_, err := Exec(context.Background(), ExecOptions{Bin: "/nonexistent/monomind", Runtime: "claude", Prompt: "hi", Access: AccessFull}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "needs Cwd") {
		t.Fatalf("err = %v", err)
	}
}

func TestLastJSONLine(t *testing.T) {
	if got := string(lastJSONLine([]byte("progress\n{\"root\":\"a\"}\n"))); got != `{"root":"a"}` {
		t.Errorf("got %q", got)
	}
}
