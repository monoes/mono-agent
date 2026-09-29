package monomind

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// writeSandboxFake writes a fake monomind that advertises (or not)
// CapAgentExecSandbox, records each agent exec argv one arg per line, and
// streams a start event carrying startExtra (e.g. `,"sandbox":"workspace"`).
func writeSandboxFake(t *testing.T, advertise bool, startExtra string) (bin, record string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	caps := `"agent-exec","agent-scan","org-json-v1"`
	if advertise {
		caps += `,"` + CapAgentExecSandbox + `"`
	}
	dir := t.TempDir()
	bin = filepath.Join(dir, "monomind")
	record = filepath.Join(dir, "argv.txt")
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"9.0.0","min_caller":"1.0.0","capabilities":[` + caps + `]}'; exit 0; fi` + "\n" +
		`if [ "$1" = "agent" ] && [ "$2" = "exec" ]; then` + "\n" +
		`  printf '%s\n' "$@" > '` + record + "'\n" +
		`  echo '{"v":1,"type":"start","runtime":"codex","streams_incrementally":false` + startExtra + `}'` + "\n" +
		`  echo '{"v":1,"type":"result","subtype":"success","stop_reason":"end_turn","text":"ok"}'` + "\n" +
		`  echo '{"v":1,"type":"done","exit_code":0}'` + "\n" +
		"  exit 0\nfi\nexit 2\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func readArgv(t *testing.T, record string) []string {
	t.Helper()
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// flagValue returns the value after flag in argv, and whether it was there.
func flagValue(argv []string, flag string) (string, bool) {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return "", false
	}
	return argv[i+1], true
}

func TestExecSandbox(t *testing.T) {
	home := os.Getenv("HOME") // testhome's throwaway HOME
	chatDir := filepath.Join(home, ".monoagent", "workspaces", WorkspaceChat)
	cases := []struct {
		name       string
		advertise  bool
		startExtra string
		opts       ExecOptions
		wantFlag   string // "" = no --sandbox
		wantCwd    string // "" = no --cwd
		wantStatus string
	}{
		{name: "sandboxed in the workspace folder", advertise: true, startExtra: `,"sandbox":"workspace"`,
			opts:     ExecOptions{Runtime: "codex", Sandbox: SandboxWorkspace, WorkspacePurpose: WorkspaceChat},
			wantFlag: SandboxWorkspace, wantCwd: chatDir, wantStatus: SandboxStatusSandboxed},
		{name: "a caller's own cwd wins", advertise: true, startExtra: `,"sandbox":"workspace"`,
			opts:     ExecOptions{Runtime: "codex", Cwd: "/profile/root", Sandbox: SandboxWorkspace, WorkspacePurpose: WorkspaceChat},
			wantFlag: SandboxWorkspace, wantCwd: "/profile/root", wantStatus: SandboxStatusSandboxed},
		{name: "runtime without a sandbox", advertise: true, startExtra: `,"sandbox":"off","sandbox_unsupported":true`,
			opts:     ExecOptions{Runtime: "copilot", Sandbox: SandboxWorkspace, WorkspacePurpose: WorkspaceAgentAsk},
			wantFlag: SandboxWorkspace, wantCwd: filepath.Join(home, ".monoagent", "workspaces", WorkspaceAgentAsk), wantStatus: SandboxStatusUnsupported},
		{name: "claude keeps its folder", advertise: true, startExtra: `,"sandbox":"workspace"`,
			opts:     ExecOptions{Runtime: "claude", Sandbox: SandboxWorkspace, WorkspacePurpose: WorkspaceChat},
			wantFlag: SandboxWorkspace, wantStatus: SandboxStatusSandboxed},
		{name: "older monomind: exactly as before", advertise: false,
			opts:       ExecOptions{Runtime: "codex", Sandbox: SandboxWorkspace, WorkspacePurpose: WorkspaceChat},
			wantStatus: SandboxStatusNeedsMonomind},
		{name: "coder mode never sandboxed", advertise: true, startExtra: `,"access":"full"`,
			opts:    ExecOptions{Runtime: "claude", Cwd: "/work", Access: AccessFull},
			wantCwd: "/work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, record := writeSandboxFake(t, tc.advertise, tc.startExtra)
			opts := tc.opts
			opts.Bin, opts.Prompt = bin, "hi"
			var start Event
			res, err := Exec(context.Background(), opts, func(ev Event) {
				if ev.Type == EventStart {
					start = ev
				}
			})
			if err != nil || res.Err != nil {
				t.Fatalf("exec: %v %+v", err, res.Err)
			}
			argv := readArgv(t, record)
			flag, hasFlag := flagValue(argv, SandboxFlag)
			if flag != tc.wantFlag || hasFlag != (tc.wantFlag != "") {
				t.Errorf("%s = %q (present %v), want %q: %v", SandboxFlag, flag, hasFlag, tc.wantFlag, argv)
			}
			cwd, hasCwd := flagValue(argv, "--cwd")
			if cwd != tc.wantCwd || hasCwd != (tc.wantCwd != "") {
				t.Errorf("--cwd = %q (present %v), want %q", cwd, hasCwd, tc.wantCwd)
			}
			if res.SandboxStatus != tc.wantStatus {
				t.Errorf("TurnResult.SandboxStatus = %q, want %q", res.SandboxStatus, tc.wantStatus)
			}
			if start.SandboxStatus != tc.wantStatus {
				t.Errorf("start event sandbox_status = %q, want %q", start.SandboxStatus, tc.wantStatus)
			}
			if tc.wantCwd != "" && strings.HasPrefix(tc.wantCwd, home) {
				if fi, err := os.Stat(tc.wantCwd); err != nil || !fi.IsDir() {
					t.Errorf("workspace %s not created: %v", tc.wantCwd, err)
				}
			}
		})
	}
}

// Without the capability no workspace folder is created either: nothing
// about the turn changes.
func TestExecSandboxWithoutCapabilityCreatesNothing(t *testing.T) {
	bin, _ := writeSandboxFake(t, false, "")
	if _, err := Exec(context.Background(), ExecOptions{Bin: bin, Runtime: "codex", Prompt: "hi",
		Sandbox: SandboxWorkspace, WorkspacePurpose: "probe-none"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".monoagent", "workspaces", "probe-none")); !os.IsNotExist(err) {
		t.Errorf("workspace created without the capability: %v", err)
	}
}

func TestStartEventSandboxFieldsRoundTrip(t *testing.T) {
	var ev Event
	if err := json.Unmarshal([]byte(`{"v":1,"type":"start","sandbox":"off","sandbox_unsupported":true}`), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Sandbox != SandboxOff || !ev.SandboxUnsupported {
		t.Fatalf("parsed %+v", ev.SandboxFields)
	}
	ev.SandboxStatus = SandboxStatusUnsupported
	b, _ := json.Marshal(ev)
	for _, want := range []string{`"sandbox":"off"`, `"sandbox_unsupported":true`, `"sandbox_status":"unsupported"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("re-encoded start lacks %s: %s", want, b)
		}
	}
	var res TurnResult
	ApplyEventToResult(&res, ev)
	if res.SandboxStatus != SandboxStatusUnsupported || !res.Sandbox.SandboxUnsupported {
		t.Errorf("ApplyEventToResult: %+v %q", res.Sandbox, res.SandboxStatus)
	}
}

func TestSandboxStatus(t *testing.T) {
	cases := []struct {
		requested string
		supported bool
		start     SandboxFields
		want      string
	}{
		{"", true, SandboxFields{Sandbox: "workspace"}, ""},
		{SandboxWorkspace, false, SandboxFields{}, SandboxStatusNeedsMonomind},
		{SandboxWorkspace, true, SandboxFields{Sandbox: "workspace"}, SandboxStatusSandboxed},
		{SandboxWorkspace, true, SandboxFields{Sandbox: "off", SandboxUnsupported: true}, SandboxStatusUnsupported},
		{SandboxWorkspace, true, SandboxFields{Sandbox: "off"}, SandboxStatusOff},
		{SandboxWorkspace, true, SandboxFields{}, SandboxStatusSandboxed},
	}
	for _, tc := range cases {
		if got := sandboxStatus(tc.requested, tc.supported, tc.start); got != tc.want {
			t.Errorf("sandboxStatus(%q, %v, %+v) = %q, want %q", tc.requested, tc.supported, tc.start, got, tc.want)
		}
	}
}
