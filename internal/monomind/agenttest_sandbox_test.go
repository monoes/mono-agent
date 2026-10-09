//go:build !windows

package monomind

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestAgentTestSandboxedPassesSandboxFlags(t *testing.T) {
	bin, argv := fakeAgentTestMonomind(t, `echo '{"v":1,"runtime":"copilot","status":"ok"}'`)
	if _, err := AgentTestSandboxed(context.Background(), bin, "copilot", "", 0, SandboxWorkspaceWrite, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(argv)
	if want := "agent test copilot --json --sandbox workspace-write --sandbox-fallback strictest"; strings.TrimSpace(string(got)) != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
	if _, err := AgentTestSandboxed(context.Background(), bin, "copilot", "", 0, "--evil", false); err == nil {
		t.Error("a sandbox mode starting with a dash must be refused")
	}
}
