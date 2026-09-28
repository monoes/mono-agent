package monomind

import (
	"context"
	"fmt"
	"os"
)

// CapOrgRoleFullAccess is monomind's per-role full access (monomind#365).
const CapOrgRoleFullAccess = "org-role-full-access"

// agentContextMarkers are the variables monomind treats as "an agent is
// running this" (its agent-context.ts): Claude Code sets the first two on
// every process a turn spawns, and org roles and agent exec set the rest.
var agentContextMarkers = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "MONOMIND_ORG_ROLE", "MONOMIND_SDK_AGENT", "MONOMIND_AGENT_EXEC"}

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
