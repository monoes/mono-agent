package health

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GroupRuntimes holds the AI agent runtimes monomind drives (claude, codex,
// opencode, …).
const GroupRuntimes = "runtimes"

// CheckRuntimes is the aggregate runtime check; each runtime is a child row
// "runtimes.<id>".
const CheckRuntimes = "runtimes.agents"

func runtimeChecks() []Check {
	return []Check{
		{ID: CheckRuntimes, Group: GroupRuntimes, Title: "AI agent runtimes", Features: []string{"agent chat", "orgs"},
			DependsOn: []string{CheckMonomindHandshake}, Timeout: 90 * time.Second, Run: checkRuntimes},
	}
}

func checkRuntimes(ctx context.Context, env *Env) Result {
	if env.ScanRuntimes == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	scan, err := env.ScanRuntimes(ctx)
	if err != nil {
		return Result{Status: StatusFail, Summary: "runtime scan failed", Detail: err.Error()}
	}
	var installed []string
	res := Result{}
	for _, a := range scan.Agents {
		child := Result{ID: GroupRuntimes + "." + a.ID, Title: a.ID}
		if a.Installed {
			installed = append(installed, a.ID)
			child.Status = StatusOK
			child.Summary = deref(a.Version, "installed")
			child.Detail = deref(a.Binary, "")
		} else {
			child.Status = StatusInfo
			child.Summary = "not installed"
			child.Detail = a.InstallHint
		}
		res.Children = append(res.Children, child)
	}
	if len(installed) == 0 {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("none of %d supported runtimes is installed", len(scan.Agents))
		res.Detail = "install one from the Agents page (e.g. Claude Code: npm install -g @anthropic-ai/claude-code)"
		return res
	}
	res.Status = StatusOK
	res.Summary = fmt.Sprintf("%d of %d installed: %s", len(installed), len(scan.Agents), strings.Join(installed, ", "))
	return res
}

func deref(s *string, def string) string {
	if s == nil || *s == "" {
		return def
	}
	return *s
}
