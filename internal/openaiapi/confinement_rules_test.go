package openaiapi

import (
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Copilot's workspace-write is allow/deny rules (approvals "on"), not an OS
// sandbox: it must not be served as Sandboxed, while codex's still is.
func TestClassifyRuntimeDemotesRuleBasedSandbox(t *testing.T) {
	caps := monomind.NewCapabilitySet("2.24.4", monomind.CapAgentExecSandbox)
	modes := []string{monomind.SandboxReadOnly, monomind.SandboxWorkspaceWrite, monomind.SandboxFull}
	report := func(approvals string) map[string]monomind.SandboxReport {
		return map[string]monomind.SandboxReport{
			monomind.SandboxWorkspaceWrite: {NativeSandbox: "workspace-write", Approvals: approvals},
		}
	}
	for name, c := range map[string]struct {
		e    monomind.ScanEntry
		want Class
	}{
		"copilot":        {monomind.ScanEntry{ID: "copilot", SandboxModes: modes, SandboxModeReports: report("on")}, Unconfined},
		"codex":          {monomind.ScanEntry{ID: "codex", SandboxModes: modes, SandboxModeReports: report("off")}, Sandboxed},
		"older monomind": {monomind.ScanEntry{ID: "copilot", SandboxModes: modes}, Sandboxed},
	} {
		if got := ClassifyRuntime(c.e, caps); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
