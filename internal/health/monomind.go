package health

import (
	"context"
	"fmt"

	"github.com/monoes/mono-agent/internal/nodemgr"
)

// GroupMonomind holds the checks for the monomind AI engine and what it
// runs on.
const GroupMonomind = "monomind"

// Check and fix IDs of the monomind group.
const (
	CheckNode      = "monomind.node"
	FixNodeInstall = "monomind.node.install"
)

// monomindFeatures is what stops working without monomind.
var monomindFeatures = []string{"orgs", "agent chat", "AI agents"}

func monomindChecks() []Check {
	return []Check{
		{ID: CheckNode, Group: GroupMonomind, Title: "Node.js", Features: monomindFeatures, Run: checkNode},
	}
}

func monomindFixes() []Fix {
	return []Fix{
		{FixInfo: FixInfo{ID: FixNodeInstall, Label: "Download a managed Node.js (latest LTS)", Safety: SafetyConfirm,
			Command: "monoagentcli nodejs install"}, Apply: fixNodeInstall},
	}
}

func checkNode(ctx context.Context, env *Env) Result {
	if env.SystemNode == nil || env.ManagedNode == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	sysPath, sysVer, sysFound := env.SystemNode(ctx)
	if sysFound && nodemgr.Suitable(sysVer) {
		return Result{Status: StatusOK, Summary: fmt.Sprintf("v%s (%s)", sysVer, sysPath)}
	}
	if v, p, ok := env.ManagedNode(); ok {
		res := Result{Status: StatusOK, Summary: fmt.Sprintf("v%s, managed by monoagent (%s)", v, p)}
		if sysFound {
			res.Detail = fmt.Sprintf("system Node v%s (%s) is older than %s and is not used", sysVer, sysPath, nodemgr.MinVersion)
		}
		return res
	}
	if sysFound {
		return Result{Status: StatusFail, Summary: fmt.Sprintf("v%s is too old — need >= %s", sysVer, nodemgr.MinVersion),
			Detail: sysPath, FixID: FixNodeInstall}
	}
	return Result{Status: StatusFail, Summary: "not found", FixID: FixNodeInstall}
}

func fixNodeInstall(ctx context.Context, env *Env, progress func(string)) error {
	if env.InstallNode == nil {
		return fmt.Errorf("installing Node.js is not available here")
	}
	return env.InstallNode(ctx, progress)
}
