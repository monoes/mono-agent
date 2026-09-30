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
	// A read-access turn (a dynamic-org research worker) is stopped the
	// same way.
	for _, access := range []string{AccessFull, AccessRead} {
		t.Run(access, func(t *testing.T) { testCancelWaitsForTreeKill(t, access) })
	}
}

func testCancelWaitsForTreeKill(t *testing.T, access string) {
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
		Exec(ctx, ExecOptions{Bin: bin, Runtime: "claude", Prompt: "p", Cwd: dir, Access: access}, nil)
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
	_, err := InitWorkspace(context.Background(), bin, dir, "claude")
	if err == nil || !strings.Contains(err.Error(), "Directory does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitWorkspaceUsesTarget(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "monomind")
	log := filepath.Join(dir, "args")
	os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" > "+log+"\necho '{\"root\":\"x\",\"created\":[],\"skipped\":[]}'\n"), 0o755)
	for target, want := range map[string]string{"claude": "--target claude", "codex": "--target codex", "agents": "--target agents"} {
		if _, err := InitWorkspace(context.Background(), bin, dir, target); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(log); !strings.Contains(string(b), want+" ") {
			t.Errorf("target %q: argv %s, want %q", target, b, want)
		}
	}
}

// A runtime without an init target (an older monomind's pi, grok, …) gets
// a minimal AGENTS.md and nothing Claude-specific; monomind is not run, and
// an AGENTS.md already there is kept.
func TestInitWorkspaceWithoutTargetWritesOnlyAgentsMD(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(t.TempDir(), "monomind")
	os.WriteFile(bin, []byte("#!/bin/sh\ntouch "+filepath.Join(dir, "CLAUDE.md")+"\necho '{}'\n"), 0o755)
	res, err := InitWorkspace(context.Background(), bin, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 1 || res.Created[0] != "AGENTS.md" || len(res.Skipped) != 0 {
		t.Errorf("result = %+v", res)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "AGENTS.md" {
		t.Fatalf("folder has %v, want AGENTS.md alone", entries)
	}
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("mine"), 0o644)
	res, err = InitWorkspace(context.Background(), bin, dir, "")
	if err != nil || len(res.Created) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("second run = %+v, %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); string(b) != "mine" {
		t.Errorf("AGENTS.md overwritten: %q", b)
	}
}

func TestCoderRuntimes(t *testing.T) {
	codex, agents := "codex", "agents"
	scan := &ScanResult{Agents: []ScanEntry{
		{ID: "claude", Installed: true, FullAccess: true, ToolActivityFidelity: "full"},
		{ID: "codex", Installed: true, FullAccess: true, ToolActivityFidelity: "full", Resume: true, Effort: true, InitTarget: &codex},
		{ID: "grok", FullAccess: true, ToolActivityFidelity: "start-only"},
		{ID: "vercel", Installed: true},
		{ID: "pi", Installed: true, FullAccess: true, InitTarget: &agents},
	}}
	byID := func(list []CoderRuntime) map[string]CoderRuntime {
		m := map[string]CoderRuntime{}
		for _, r := range list {
			m[r.ID] = r
		}
		return m
	}

	old := byID(CoderRuntimes(scan, NewCapabilitySet("2.18.3", CoderCapabilities...)))
	want := CoderRuntime{ID: "claude", Installed: true, FullAccess: true, Ready: true, ToolActivity: "full", Resume: true, Effort: true, MaxTurns: true, ReportsCost: true, InitTarget: "claude"}
	if old["claude"] != want {
		t.Errorf("old monomind claude = %+v", old["claude"])
	}
	if old["codex"].FullAccess || old["codex"].Ready || old["grok"].FullAccess {
		t.Errorf("old monomind gave full access beyond claude: %+v", old)
	}

	all := byID(CoderRuntimes(scan, NewCapabilitySet("2.19.0", append(CoderCapabilities, CapAgentExecFullAccessAny)...)))
	if c := all["codex"]; !c.Ready || c.InitTarget != "codex" || !c.Resume || c.MaxTurns {
		t.Errorf("codex = %+v", c)
	}
	if g := all["grok"]; !g.FullAccess || g.Ready || g.ToolActivity != "start-only" {
		t.Errorf("grok = %+v", g)
	}
	if p, g := all["pi"], all["grok"]; p.InitTarget != "agents" || g.InitTarget != "" {
		t.Errorf("pi = %+v, grok = %+v: no runtime but claude falls back to claude's setup", p, g)
	}
	if v := all["vercel"]; v.FullAccess || v.Ready || v.ToolActivity != "none" {
		t.Errorf("vercel = %+v", v)
	}
	if c := all["claude"]; c.Resume || c.InitTarget != "claude" || !c.Ready {
		t.Errorf("new monomind's claude is taken as scanned: %+v", c)
	}

	noCaps := byID(CoderRuntimes(scan, NewCapabilitySet("2.19.0", CapAgentExecFullAccessAny)))
	if noCaps["codex"].Ready || !noCaps["codex"].FullAccess {
		t.Errorf("ready without the coder capabilities: %+v", noCaps["codex"])
	}

	if got := CoderRuntimes(nil, nil); len(got) != 1 || got[0].ID != "claude" || !got[0].FullAccess || got[0].Installed {
		t.Errorf("no scan = %+v", got)
	}
	if FindCoderRuntime(CoderRuntimes(scan, nil), "grok") == nil || FindCoderRuntime(nil, "x") != nil {
		t.Error("FindCoderRuntime")
	}
}

func TestScanEntryCoderFields(t *testing.T) {
	var e ScanEntry
	json.Unmarshal([]byte(`{"id":"codex","installed":true,"full_access":true,"tool_activity_fidelity":"full","resume":true,"effort":true,"max_turns":false,"reports_cost":true,"init_target":"codex"}`), &e)
	if !e.FullAccess || e.ToolActivityFidelity != "full" || !e.Resume || !e.Effort || e.MaxTurns || !e.ReportsCost || e.InitTarget == nil || *e.InitTarget != "codex" {
		t.Errorf("decoded = %+v", e)
	}
	var old ScanEntry
	json.Unmarshal([]byte(`{"id":"pi","installed":true,"init_target":null}`), &old)
	if old.FullAccess || old.InitTarget != nil {
		t.Errorf("older scan entry = %+v", old)
	}
}

func TestToolActivityKindAndExitCode(t *testing.T) {
	var ev Event
	json.Unmarshal([]byte(`{"v":1,"type":"tool_activity","id":"c1","phase":"end","kind":"shell","exit_code":0}`), &ev)
	if ev.Kind != "shell" || !ev.HasExitCode || ev.ExitCode != 0 {
		t.Fatalf("decoded = %+v", ev)
	}
	b, _ := json.Marshal(ev)
	if !strings.Contains(string(b), `"exit_code":0`) || !strings.Contains(string(b), `"kind":"shell"`) {
		t.Errorf("re-encode lost exit_code 0 or kind: %s", b)
	}
	var none Event
	json.Unmarshal([]byte(`{"v":1,"type":"tool_activity","id":"c1","phase":"end"}`), &none)
	if none.HasExitCode {
		t.Error("absent exit_code reported as present")
	}
	if b, _ := json.Marshal(none); strings.Contains(string(b), "exit_code") {
		t.Errorf("absent exit_code gained one: %s", b)
	}
}

// TestExecEffort: --effort when monomind maps it per runtime, else the
// CLAUDE_EFFORT env for claude only.
func TestExecEffort(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "monomind")
	record := filepath.Join(dir, "argv")
	os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\" > "+record+"\n"+
		`echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"ok"}'`+"\n"+
		`echo '{"v":1,"type":"done","exit_code":0}'`+"\n"), 0o755)
	for _, c := range []struct {
		runtime string
		flag    bool
		want    string
		never   string
	}{
		{"claude", true, "--effort high", "CLAUDE_EFFORT"},
		{"codex", true, "--effort high", "CLAUDE_EFFORT"},
		{"claude", false, "--env CLAUDE_EFFORT=high", "--effort"},
		{"codex", false, "", "effort"},
	} {
		if _, err := Exec(context.Background(), ExecOptions{Bin: bin, Runtime: c.runtime, Prompt: "p", Effort: "high", EffortFlag: c.flag}, nil); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(record)
		argv := string(b)
		if !strings.Contains(argv, c.want) || strings.Contains(strings.ToLower(argv), strings.ToLower(c.never)) {
			t.Errorf("%s flag=%v: argv %s", c.runtime, c.flag, argv)
		}
	}
}
