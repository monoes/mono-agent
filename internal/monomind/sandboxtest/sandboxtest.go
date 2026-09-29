// Package sandboxtest is a fake monomind for the tests that check each
// agent exec call site asks for the sandbox (monomind.ExecOptions.Sandbox)
// only when monomind advertises it.
package sandboxtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Install writes a fake monomind, points discovery (monomind.EnvOverride)
// and HOME (sandbox workspaces live under it) at throwaway folders, and
// returns the file every agent exec argv is appended to. The fake
// advertises monomind.CapAgentExecSandbox when advertise is set, and
// answers each turn with reply as the result text.
func Install(t *testing.T, advertise bool, reply string) (argsLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	t.Setenv("HOME", t.TempDir())
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
	caps := `"agent-exec","agent-scan","org-json-v1"`
	sandbox := `,"sandbox":"off"`
	if advertise {
		caps += `,"` + monomind.CapAgentExecSandbox + `"`
		sandbox = `,"sandbox":"workspace"`
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "monomind")
	argsLog = filepath.Join(dir, "exec-args.log")
	replyFile := filepath.Join(dir, "reply.txt")
	quoted, _ := json.Marshal(reply)
	if err := os.WriteFile(replyFile, quoted, 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"9.0.0","min_caller":"1.0.0","capabilities":[` + caps + `]}'; exit 0; fi` + "\n" +
		`if [ "$1" = "agent" ] && [ "$2" = "exec" ]; then` + "\n" +
		`  echo "$*" >> '` + argsLog + "'\n" +
		`  echo '{"v":1,"type":"start","runtime":"codex"` + sandbox + `}'` + "\n" +
		`  printf '{"v":1,"type":"result","subtype":"success","stop_reason":"end_turn","text":%s}\n' "$(cat '` + replyFile + `')"` + "\n" +
		`  echo '{"v":1,"type":"done","exit_code":0}'` + "\n" +
		"  exit 0\nfi\nexit 2\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	return argsLog
}

// ExecLines returns every recorded agent exec argv, one line per turn.
func ExecLines(t *testing.T, argsLog string) []string {
	t.Helper()
	raw, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("no agent exec ran: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// Check asserts every recorded turn passed `--sandbox workspace` when
// advertise is set and never passed it otherwise, and, when wantCwd is
// not empty and the flag is expected, that it ran in that folder.
func Check(t *testing.T, argsLog string, advertise bool, wantCwd string) {
	t.Helper()
	flag := monomind.SandboxFlag + " " + monomind.SandboxWorkspace
	for _, line := range ExecLines(t, argsLog) {
		if got := strings.Contains(line, monomind.SandboxFlag); got != advertise {
			t.Errorf("%s present = %v, want %v: %s", monomind.SandboxFlag, got, advertise, line)
		}
		if advertise && !strings.Contains(line, flag) {
			t.Errorf("argv lacks %q: %s", flag, line)
		}
		if advertise && wantCwd != "" && !strings.Contains(line, "--cwd "+wantCwd) {
			t.Errorf("argv lacks --cwd %s: %s", wantCwd, line)
		}
	}
}

// Workspace is the sandbox workspace folder for purpose under the HOME
// Install set.
func Workspace(purpose string) string {
	return filepath.Join(os.Getenv("HOME"), ".monoagent", "workspaces", purpose)
}
