package monomind

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
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

// TestExecFullAccessCancelWaitsForMonomindsOwnTreeKill: on cancel a
// full-access turn SIGTERMs monomind and gives it FullAccessKillGrace to
// kill the agent's tree itself, instead of the scoped KillGrace SIGKILL
// that would cut monomind off mid-cleanup and orphan the agent.
func TestExecFullAccessCancelWaitsForMonomindsOwnTreeKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals are unix-only in this build")
	}
	oldGrace, oldFull := KillGrace, FullAccessKillGrace
	KillGrace, FullAccessKillGrace = 100*time.Millisecond, 5*time.Second
	defer func() { KillGrace, FullAccessKillGrace = oldGrace, oldFull }()

	dir := t.TempDir()
	marker := filepath.Join(dir, "cleaned-up")
	ready := filepath.Join(dir, "ready")
	bin := filepath.Join(dir, "monomind")
	// Stands in for monomind: on SIGTERM it "kills the agent tree" (takes
	// 1s), records that it finished, and exits.
	script := "#!/bin/sh\n" +
		"trap 'sleep 1; touch " + marker + "; exit 130' TERM\n" +
		`echo '{"v":1,"type":"start","runtime":"claude","cwd":"/w","pid":1,"access":"full"}'` + "\n" +
		"touch " + ready + "\n" +
		"while true; do sleep 0.1; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Exec(ctx, ExecOptions{Bin: bin, Runtime: "claude", Prompt: "p", Cwd: dir, Access: AccessFull}, nil)
		close(done)
	}()
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Exec did not return after cancel")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("monomind was killed before it finished stopping the agent's tree")
	}
}

func TestInitWorkspaceReportsJSONError(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "monomind")
	os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"success\":false,\"error\":\"Directory does not exist: /nope\"}'\nexit 1\n"), 0o755)
	_, err := InitWorkspace(context.Background(), bin, dir)
	if err == nil || !strings.Contains(err.Error(), "Directory does not exist") {
		t.Fatalf("err = %v", err)
	}
}
