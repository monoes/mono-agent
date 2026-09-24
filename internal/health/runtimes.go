package health

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/agentinstall"
)

// GroupRuntimes holds the AI agent runtimes monomind drives (claude, codex,
// opencode, …).
const GroupRuntimes = "runtimes"

// CheckRuntimes is the aggregate runtime check; each runtime is a child row
// "runtimes.<id>".
const CheckRuntimes = "runtimes.agents"

// FixRuntimeInstall installs one runtime: "runtimes.install:<id>".
const FixRuntimeInstall = "runtimes.install"

func runtimeFixes() []Fix {
	return []Fix{{
		FixInfo: FixInfo{ID: FixRuntimeInstall, Label: "Install {arg}", Safety: SafetyConfirm,
			Command: "monoagentcli agent install {arg}", Optional: true},
		ApplyArg: func(ctx context.Context, env *Env, id string, progress func(string)) error {
			if env.InstallRuntime == nil {
				return fmt.Errorf("installing runtimes is not available here")
			}
			return env.InstallRuntime(ctx, id, progress)
		},
	}}
}

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
			if rec := agentinstall.ForEntry(a); rec.Kind != agentinstall.KindManual {
				child.FixID = FixRuntimeInstall + ":" + a.ID
				child.FixCommand = installCommand(rec, a.ID)
			}
		}
		res.Children = append(res.Children, child)
	}
	if len(installed) == 0 {
		res.Status = StatusFail
		res.Summary = fmt.Sprintf("none of %d supported runtimes is installed", len(scan.Agents))
		res.Detail = "install one: monoagentcli agent install <runtime> (e.g. claude), or from the Agents page"
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

// installCommand says what installing a runtime really runs, for the fix's
// confirmation: the vendor script's URL and shell, or the npm packages.
func installCommand(r agentinstall.Recipe, id string) string {
	switch r.Kind {
	case agentinstall.KindScript:
		return fmt.Sprintf("downloads and runs the vendor installer %s with %s (monoagentcli agent install %s)", r.ScriptURL, r.Shell, id)
	case agentinstall.KindNpm:
		return fmt.Sprintf("npm install -g %s (monoagentcli agent install %s)", strings.Join(r.Packages, " "), id)
	}
	return "monoagentcli agent install " + id
}
