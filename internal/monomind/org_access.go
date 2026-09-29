package monomind

import (
	"context"
	"fmt"
	"os"
)

// CapOrgRoleFullAccess is monomind's per-role full access (monomind#365).
const CapOrgRoleFullAccess = "org-role-full-access"

// agentContextMarkers are the variables monomind treats as "an agent is
// running this" (AGENT_CONTEXT_ENV_MARKERS in its agent-context.ts; keep the
// two lists the same): Claude Code sets the first two on every process a
// turn spawns, and org roles and agent exec set the three MONOMIND_* ones
// (agent exec on every runtime's child). AI_AGENT and AGENT are cross-vendor
// markers (pi, crush, Claude Code; opencode, crush). The rest are what the
// other coding CLIs set on the commands their own shell tool runs, so a
// grant is refused inside them even when they were started outside
// monomind: Codex, OpenCode, Antigravity, Gemini CLI, Grok, Copilot, Crush,
// pi (PI_SESSION_ID on its bash tool's commands), Qwen Code and DeepSeek
// Harness (DSH_SHELL, DSH_SESSION_ID on its shell calls). Cline and aider
// set none of their own, so monomind's runners set MONOMIND_CLINE_TURN and
// MONOMIND_AIDER on them.
var agentContextMarkers = []string{
	"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT",
	"MONOMIND_ORG_ROLE", "MONOMIND_SDK_AGENT", "MONOMIND_AGENT_EXEC",
	"AI_AGENT", "AGENT",
	"CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CODEX_THREAD_ID", "CODEX_CI",
	"OPENCODE", "OPENCODE_PID", "ANTIGRAVITY_AGENT", "GEMINI_CLI",
	"GROK_SESSION_ID", "GROK_MANAGED_BY_NPM",
	"COPILOT_CLI_BINARY_VERSION", "COPILOT_AGENT_SESSION_ID",
	"CRUSH", "PI_CODING_AGENT", "PI_SESSION_ID", "QWEN_CODE",
	"DSH_SHELL", "DSH_SESSION_ID", "MONOMIND_CLINE_TURN", "MONOMIND_AIDER",
}

// AgentContextMarker returns the first agent-context variable set in this
// process's environment, "" when none is.
func AgentContextMarker() string {
	for _, k := range agentContextMarkers {
		if os.Getenv(k) != "" {
			return k
		}
	}
	return ""
}

// OrgRoleSetAccess grants ("full") or revokes ("scoped") a role's full
// access. A grant is human-only: it is refused here when this process runs
// inside an agent, before monomind is called, and monomind checks the same
// markers again. The environment is passed through unfiltered for that
// reason — FilteredEnviron would strip the markers and launder the grant.
func OrgRoleSetAccess(ctx context.Context, projectRoot, org, role, access string) (string, error) {
	args := []string{"role", "set-access", org, role, access}
	switch access {
	case AccessScoped:
	case AccessFull:
		if m := AgentContextMarker(); m != "" {
			return "", fmt.Errorf("granting full access is human-only and this looks like an agent (%s is set); run it yourself from a terminal or the app", m)
		}
		args = append(args, "--yes-i-understand")
	default:
		return "", fmt.Errorf("access must be %q or %q, got %q", AccessFull, AccessScoped, access)
	}
	return runOrgText(ctx, projectRoot, args...)
}
