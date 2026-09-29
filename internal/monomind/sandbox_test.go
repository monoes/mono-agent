package monomind

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// writeSandboxFake writes a fake monomind that advertises (or not)
// CapAgentExecSandbox, records each agent exec argv one arg per line, and
// streams a start event carrying startExtra (e.g. `,"sandbox":"workspace-write"`).
func writeSandboxFake(t *testing.T, version string, advertise bool, startExtra string) (bin, record string) {
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
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + version + `","min_caller":"1.0.0","capabilities":[` + caps + `]}'; exit 0; fi` + "\n" +
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

// sandboxPairs returns the argv's --sandbox and --env pairs, in order.
func sandboxPairs(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == SandboxFlag || argv[i] == "--env" {
			out = append(out, argv[i]+" "+argv[i+1])
		}
	}
	return out
}

func TestSandboxArgs(t *testing.T) {
	future := NewCapabilitySet("9.0.0", CapAgentExecSandbox)
	today := NewCapabilitySet("2.18.5")
	floor := NewCapabilitySet(SandboxEnvMinVersion)
	old := NewCapabilitySet("2.10.0")
	flag := []string{SandboxFlag, SandboxWorkspaceWrite}
	env := []string{"--env", "MONOMIND_GIT_LEVEL=read"}
	cases := []struct {
		caps          *CapabilitySet
		runtime, mode string
		want          []string
		effective     string
	}{
		// monomind#396: the flag, for every runtime.
		{future, "codex", SandboxWorkspaceWrite, flag, SandboxStatusSandboxed},
		{future, "copilot", SandboxWorkspaceWrite, flag, SandboxStatusSandboxed},
		{future, "claude", SandboxWorkspaceWrite, flag, SandboxStatusSandboxed},
		{future, "codex", SandboxReadOnly, []string{SandboxFlag, SandboxReadOnly}, SandboxStatusSandboxed},
		{future, "codex", SandboxFull, []string{SandboxFlag, SandboxFull}, SandboxStatusOff},
		// Today: the env path for codex and grok.
		{today, "codex", SandboxWorkspaceWrite, env, SandboxStatusSandboxed},
		{today, "grok", SandboxWorkspaceWrite, env, SandboxStatusSandboxed},
		{floor, "codex", SandboxWorkspaceWrite, env, SandboxStatusSandboxed},
		{today, "codex", SandboxReadOnly, nil, SandboxStatusAwaitingMonomind},
		{today, "copilot", SandboxWorkspaceWrite, nil, SandboxStatusAwaitingMonomind},
		{today, "qwen", SandboxWorkspaceWrite, nil, SandboxStatusAwaitingMonomind},
		{today, "antigravity", SandboxWorkspaceWrite, nil, SandboxStatusAwaitingMonomind},
		{today, "claude", SandboxWorkspaceWrite, nil, SandboxStatusScoped},
		{today, "codex", SandboxFull, nil, SandboxStatusOff},
		// Older monomind, or no handshake: exactly as before.
		{old, "codex", SandboxWorkspaceWrite, nil, SandboxStatusNeedsMonomind},
		{nil, "grok", SandboxWorkspaceWrite, nil, SandboxStatusNeedsMonomind},
		// Coder mode asks for nothing.
		{future, "claude", "", nil, ""},
		{today, "codex", "", nil, ""},
	}
	for _, tc := range cases {
		v := "<nil>"
		if tc.caps != nil {
			v = tc.caps.Version + fmt.Sprint(tc.caps.List())
		}
		args, eff := SandboxArgs(tc.caps, tc.runtime, tc.mode)
		if !slices.Equal(args, tc.want) || eff != tc.effective {
			t.Errorf("SandboxArgs(%s, %s, %q) = %v, %q; want %v, %q", v, tc.runtime, tc.mode, args, eff, tc.want, tc.effective)
		}
	}
}

func TestExecSandbox(t *testing.T) {
	home := os.Getenv("HOME") // testhome's throwaway HOME
	ws := func(p string) string { return filepath.Join(home, ".monoagent", "workspaces", p) }
	mode := SandboxWorkspaceWrite
	cases := []struct {
		name       string
		version    string
		advertise  bool
		startExtra string
		opts       ExecOptions
		wantPairs  []string // --sandbox / --env pairs, exactly
		wantCwd    string   // "" = no --cwd
		wantStatus string
	}{
		{name: "monomind 396 flag", version: "9.0.0", advertise: true, startExtra: `,"sandbox":"workspace-write"`,
			opts:      ExecOptions{Runtime: "codex", Sandbox: mode, WorkspacePurpose: WorkspaceChat},
			wantPairs: []string{"--sandbox workspace-write"}, wantCwd: ws(WorkspaceChat), wantStatus: SandboxStatusSandboxed},
		{name: "monomind 396, runtime cannot", version: "9.0.0", advertise: true, startExtra: `,"sandbox":"full","sandbox_unsupported":true`,
			opts:      ExecOptions{Runtime: "copilot", Sandbox: mode, WorkspacePurpose: WorkspaceAgentAsk},
			wantPairs: []string{"--sandbox workspace-write"}, wantCwd: ws(WorkspaceAgentAsk), wantStatus: SandboxStatusUnsupported},
		{name: "env workaround: codex", version: "2.18.5",
			opts:      ExecOptions{Runtime: "codex", Sandbox: mode, WorkspacePurpose: WorkspaceChat},
			wantPairs: []string{"--env MONOMIND_GIT_LEVEL=read"}, wantCwd: ws(WorkspaceChat), wantStatus: SandboxStatusSandboxed},
		{name: "env workaround: grok keeps a caller's cwd", version: "2.18.5",
			opts:      ExecOptions{Runtime: "grok", Cwd: "/profile/root", Sandbox: mode, WorkspacePurpose: WorkspaceChat},
			wantPairs: []string{"--env MONOMIND_GIT_LEVEL=read"}, wantCwd: "/profile/root", wantStatus: SandboxStatusSandboxed},
		{name: "env workaround alongside claude effort", version: "2.18.5",
			opts:      ExecOptions{Runtime: "claude", Effort: "high", Sandbox: mode, WorkspacePurpose: WorkspaceChat},
			wantPairs: []string{"--env CLAUDE_EFFORT=high"}, wantStatus: SandboxStatusScoped},
		{name: "not covered until monomind 396", version: "2.18.5",
			opts:       ExecOptions{Runtime: "qwen", Sandbox: mode, WorkspacePurpose: WorkspaceChat},
			wantStatus: SandboxStatusAwaitingMonomind},
		{name: "older monomind: exactly as before", version: "2.10.0",
			opts:       ExecOptions{Runtime: "codex", Sandbox: mode, WorkspacePurpose: WorkspaceChat},
			wantStatus: SandboxStatusNeedsMonomind},
		{name: "coder mode never sandboxed", version: "9.0.0", advertise: true, startExtra: `,"access":"full"`,
			opts:    ExecOptions{Runtime: "claude", Cwd: "/work", Access: AccessFull},
			wantCwd: "/work"},
		{name: "coder mode never sandboxed (env path)", version: "2.18.5",
			opts:    ExecOptions{Runtime: "codex", Cwd: "/work", Access: AccessFull},
			wantCwd: "/work"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin, record := writeSandboxFake(t, tc.version, tc.advertise, tc.startExtra)
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
			if got := sandboxPairs(argv); !slices.Equal(got, tc.wantPairs) {
				t.Errorf("sandbox args = %v, want %v (argv %v)", got, tc.wantPairs, argv)
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
			if strings.HasPrefix(tc.wantCwd, home) {
				if fi, err := os.Stat(tc.wantCwd); err != nil || !fi.IsDir() {
					t.Errorf("workspace %s not created: %v", tc.wantCwd, err)
				}
			}
		})
	}
}

// When nothing is passed, no workspace folder is created either: nothing
// about the turn changes.
func TestExecSandboxWithoutSupportCreatesNothing(t *testing.T) {
	bin, _ := writeSandboxFake(t, "2.10.0", false, "")
	if _, err := Exec(context.Background(), ExecOptions{Bin: bin, Runtime: "codex", Prompt: "hi",
		Sandbox: SandboxWorkspaceWrite, WorkspacePurpose: "probe-none"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".monoagent", "workspaces", "probe-none")); !os.IsNotExist(err) {
		t.Errorf("workspace created without a sandbox: %v", err)
	}
}

func TestStartEventSandboxFieldsRoundTrip(t *testing.T) {
	var ev Event
	if err := json.Unmarshal([]byte(`{"v":1,"type":"start","sandbox":"full","sandbox_unsupported":true}`), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Sandbox != SandboxFull || !ev.SandboxUnsupported {
		t.Fatalf("parsed %+v", ev.SandboxFields)
	}
	ev.SandboxStatus = SandboxStatusUnsupported
	b, _ := json.Marshal(ev)
	for _, want := range []string{`"sandbox":"full"`, `"sandbox_unsupported":true`, `"sandbox_status":"unsupported"`} {
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
		effective string
		start     SandboxFields
		want      string
	}{
		{"", SandboxFields{Sandbox: "workspace-write"}, ""},
		{SandboxStatusSandboxed, SandboxFields{}, SandboxStatusSandboxed}, // env path: no report
		{SandboxStatusScoped, SandboxFields{}, SandboxStatusScoped},
		{SandboxStatusSandboxed, SandboxFields{Sandbox: "workspace-write"}, SandboxStatusSandboxed},
		{SandboxStatusSandboxed, SandboxFields{Sandbox: "full", SandboxUnsupported: true}, SandboxStatusUnsupported},
		{SandboxStatusSandboxed, SandboxFields{Sandbox: "full"}, SandboxStatusOff},
	}
	for _, tc := range cases {
		if got := sandboxStatus(tc.effective, tc.start); got != tc.want {
			t.Errorf("sandboxStatus(%q, %+v) = %q, want %q", tc.effective, tc.start, got, tc.want)
		}
	}
}
