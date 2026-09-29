// Package sandboxtest is a fake monomind for the tests that check each
// agent exec call site asks for the sandbox (monomind.ExecOptions.Sandbox)
// and gets the args monomind.SandboxArgs picks for a codex turn.
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

// Monomind is which monomind the fake stands in for.
type Monomind int

const (
	Future Monomind = iota // advertises monomind.CapAgentExecSandbox (monomind#396)
	Today                  // 2.18.5: codex and grok sandboxed through --env
	Old                    // 2.10.0: no sandbox at all
)

// Kinds lists every kind, for table tests.
var Kinds = []Monomind{Future, Today, Old}

func (m Monomind) String() string { return [...]string{"future", "today", "old"}[m] }

// Install writes a fake monomind of kind m, points discovery
// (monomind.EnvOverride) and HOME (sandbox workspaces live under it) at
// throwaway folders, and returns the file every agent exec argv is
// appended to. The fake answers each turn with reply as the result text.
func Install(t *testing.T, m Monomind, reply string) (argsLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	t.Setenv("HOME", t.TempDir())
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
	caps := `"agent-exec","agent-scan","org-json-v1"`
	version, sandbox := "2.18.5", ""
	switch m {
	case Future:
		caps += `,"` + monomind.CapAgentExecSandbox + `"`
		version, sandbox = "9.0.0", `,"sandbox":"`+monomind.SandboxWorkspaceWrite+`"`
	case Old:
		version = "2.10.0"
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
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"` + version + `","min_caller":"1.0.0","capabilities":[` + caps + `]}'; exit 0; fi` + "\n" +
		// agent scan answers like monomind 2.19.0: codex and grok have a
		// sandbox of their own, every other runtime lists only "full".
		`if [ "$1" = "agent" ] && [ "$2" = "scan" ]; then echo '{"v":1,"agents":[` +
		`{"id":"codex","installed":true,"sandbox_modes":["read-only","workspace-write","full"]},` +
		`{"id":"grok","installed":true,"sandbox_modes":["read-only","workspace-write","full"]},` +
		`{"id":"claude","installed":true,"sandbox_modes":["full"]},` +
		`{"id":"copilot","installed":true,"sandbox_modes":["full"]}]}'; exit 0; fi` + "\n" +
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

// Check asserts every recorded codex turn carried exactly the sandbox
// args monomind.SandboxArgs gives for m and, when wantCwd is not empty and
// the turn was sandboxed, that it ran in that folder.
func Check(t *testing.T, argsLog string, m Monomind, wantCwd string) {
	t.Helper()
	want := map[Monomind]string{
		Future: monomind.SandboxFlag + " " + monomind.SandboxWorkspaceWrite,
		Today:  "--env MONOMIND_GIT_LEVEL=read",
	}[m]
	for _, line := range ExecLines(t, argsLog) {
		if want != "" && !strings.Contains(line, want) {
			t.Errorf("%s monomind: argv lacks %q: %s", m, want, line)
		}
		if m != Future && strings.Contains(line, monomind.SandboxFlag) {
			t.Errorf("%s monomind: argv has %s: %s", m, monomind.SandboxFlag, line)
		}
		if m == Old && strings.Contains(line, "MONOMIND_GIT_LEVEL") {
			t.Errorf("old monomind: argv has the env workaround: %s", line)
		}
		if want != "" && wantCwd != "" && !strings.Contains(line, "--cwd "+wantCwd) {
			t.Errorf("%s monomind: argv lacks --cwd %s: %s", m, wantCwd, line)
		}
	}
}

// Status is the verdict a codex turn gets from monomind kind m.
func Status(m Monomind) string {
	if m == Old {
		return monomind.SandboxStatusNeedsMonomind
	}
	return monomind.SandboxStatusSandboxed
}

// Workspace is the sandbox workspace folder for purpose under the HOME
// Install set.
func Workspace(purpose string) string {
	return filepath.Join(os.Getenv("HOME"), ".monoagent", "workspaces", purpose)
}
