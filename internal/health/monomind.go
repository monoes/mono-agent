package health

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/nodemgr"
)

// GroupMonomind holds the checks for the monomind AI engine and what it
// runs on.
const GroupMonomind = "monomind"

// Check and fix IDs of the monomind group.
const (
	CheckNode                 = "monomind.node"
	CheckMonomindBinary       = "monomind.binary"
	CheckMonomindHandshake    = "monomind.handshake"
	CheckMonomindCapabilities = "monomind.capabilities"
	CheckMonomindProfileInit  = "monomind.profile_init"

	FixNodeInstall         = "monomind.node.install"
	ActionNodeUpdate       = "monomind.node.update"
	ActionNodeRemove       = "monomind.node.remove"
	FixMonomindInstall     = "monomind.install"
	FixMonomindProfileInit = "monomind.profile_init"
)

// MonomindPackage is the npm package monomind ships as.
const MonomindPackage = "@monoes/monomindcli@latest"

// optionalCapabilities maps each optional monomind capability to the
// feature that needs it (internal/monomind/capabilities.go).
var optionalCapabilities = []struct{ cap, feature string }{
	{monomind.CapOrgToolProviders, "org grants, boss/parent deciders, live org send"},
	{monomind.CapOrgEndpointRoles, "automation roles"},
	{monomind.CapOrgFederation, "cross-root org restrictions"},
	{monomind.CapOrgDecisionAttribution, "decision attribution (--by)"},
}

// monomindFeatures is what stops working without monomind.
var monomindFeatures = []string{"orgs", "agent chat", "AI agents"}

func monomindChecks() []Check {
	return []Check{
		{ID: CheckNode, Group: GroupMonomind, Title: "Node.js", Features: monomindFeatures, Run: checkNode},
		{ID: CheckMonomindBinary, Group: GroupMonomind, Title: "monomind", Features: monomindFeatures,
			DependsOn: []string{CheckNode}, Run: checkMonomindBinary},
		{ID: CheckMonomindHandshake, Group: GroupMonomind, Title: "monomind version", Features: monomindFeatures,
			DependsOn: []string{CheckMonomindBinary}, Timeout: 30 * time.Second, Run: checkMonomindHandshake},
		{ID: CheckMonomindCapabilities, Group: GroupMonomind, Title: "monomind features",
			DependsOn: []string{CheckMonomindHandshake}, Timeout: 30 * time.Second, Run: checkMonomindCapabilities},
		{ID: CheckMonomindProfileInit, Group: GroupMonomind, Title: "monomind profile", Features: []string{"orgs", "memory", "knowledge graph"},
			DependsOn: []string{CheckMonomindHandshake, CheckProfile}, Run: checkMonomindProfileInit},
	}
}

func monomindFixes() []Fix {
	return []Fix{
		{FixInfo: FixInfo{ID: FixNodeInstall, Label: "Download a managed Node.js (latest LTS)", Safety: SafetyConfirm,
			Command: "monoagentcli nodejs install"}, Apply: fixNodeInstall},
		{FixInfo: FixInfo{ID: ActionNodeUpdate, Label: "Update managed Node.js", Safety: SafetyConfirm,
			Command: "monoagentcli nodejs update", Optional: true}, Apply: func(ctx context.Context, env *Env, progress func(string)) error {
			if env.UpdateNode == nil {
				return fmt.Errorf("not available here")
			}
			return env.UpdateNode(ctx, progress)
		}},
		{FixInfo: FixInfo{ID: ActionNodeRemove, Label: "Remove managed Node.js", Safety: SafetyConfirm,
			Command: "monoagentcli nodejs remove --yes", Optional: true}, Apply: func(ctx context.Context, env *Env, progress func(string)) error {
			if env.RemoveNode == nil {
				return fmt.Errorf("not available here")
			}
			return env.RemoveNode(ctx, progress)
		}},
		{FixInfo: FixInfo{ID: FixMonomindInstall, Label: "Install / update monomind", Safety: SafetyConfirm,
			Command: "npm install -g " + MonomindPackage}, Apply: fixMonomindInstall},
		{FixInfo: FixInfo{ID: FixMonomindProfileInit, Label: "Set up this profile's folder for monomind", Safety: SafetyConfirm,
			Command: "monomind init --yes --no-watch --no-install (in the profile folder)"}, Apply: fixMonomindProfileInit},
	}
}

func checkNode(ctx context.Context, env *Env) Result {
	if env.SystemNode == nil || env.ManagedNode == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	sysPath, sysVer, sysFound := env.SystemNode(ctx)
	managedV, managedP, managed := env.ManagedNode()
	if sysFound && nodemgr.Suitable(sysVer) {
		res := Result{Status: StatusOK, Summary: fmt.Sprintf("v%s (%s)", sysVer, sysPath)}
		if managed {
			// A managed Node is installed but unused; it can go.
			res.Detail = fmt.Sprintf("a managed Node v%s is also installed (%s) but not used", managedV, managedP)
			res.ActionIDs = []string{ActionNodeUpdate, ActionNodeRemove}
		}
		return res
	}
	if managed {
		v, p := managedV, managedP
		// It is the Node in use: update, but no remove (that would break monomind).
		res := Result{Status: StatusOK, Summary: fmt.Sprintf("v%s, managed by monoagent (%s)", v, p), ActionIDs: []string{ActionNodeUpdate}}
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

func checkMonomindBinary(_ context.Context, env *Env) Result {
	if env.FindMonomind == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	bin, err := env.FindMonomind()
	if err != nil {
		res := Result{Status: StatusFail, Summary: "not installed", Detail: err.Error()}
		if monomind.IsNotFound(err) {
			res.FixID = FixMonomindInstall
		}
		return res
	}
	return Result{Status: StatusOK, Summary: bin}
}

func checkMonomindHandshake(ctx context.Context, env *Env) Result {
	if env.MonomindHandshake == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	vi, err := env.MonomindHandshake(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 127") {
			// The `#!/usr/bin/env node` shebang could not find node.
			return Result{Status: StatusFail, Summary: "cannot start — `node` is not on PATH", Detail: err.Error(), FixID: FixNodeInstall}
		}
		return Result{Status: StatusFail, Summary: "unusable — needs an update", Detail: err.Error(), FixID: FixMonomindInstall}
	}
	return Result{Status: StatusOK, Summary: fmt.Sprintf("v%s (protocol v%d, need >= %s)", vi.Version, vi.V, monomind.MinMonomindVersion)}
}

func checkMonomindCapabilities(ctx context.Context, env *Env) Result {
	if env.MonomindHandshake == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	vi, err := env.MonomindHandshake(ctx)
	if err != nil {
		return Result{Status: StatusSkip, Summary: "handshake failed"}
	}
	var missing []string
	for _, oc := range optionalCapabilities {
		if !vi.HasCapability(oc.cap) {
			missing = append(missing, fmt.Sprintf("%s — %s", oc.cap, oc.feature))
		}
	}
	if len(missing) > 0 {
		return Result{Status: StatusWarn, Summary: fmt.Sprintf("%d feature(s) disabled until monomind is updated", len(missing)),
			Detail: strings.Join(missing, "\n"), FixID: FixMonomindInstall}
	}
	return Result{Status: StatusOK, Summary: fmt.Sprintf("all %d optional features available", len(optionalCapabilities))}
}

func checkMonomindProfileInit(_ context.Context, env *Env) Result {
	if env.ProfileRoot == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	root := env.ProfileRoot(env.profileID())
	if monomind.IsInitializedAt(root) {
		return Result{Status: StatusOK, Summary: root}
	}
	return Result{Status: StatusWarn, Summary: "not set up yet: " + root, FixID: FixMonomindProfileInit}
}

func fixMonomindInstall(ctx context.Context, env *Env, progress func(string)) error {
	if env.InstallMonomind == nil {
		return fmt.Errorf("installing monomind is not available here")
	}
	return env.InstallMonomind(ctx, progress)
}

func fixMonomindProfileInit(ctx context.Context, env *Env, progress func(string)) error {
	if env.InitMonomindProfile == nil || env.ProfileRoot == nil {
		return fmt.Errorf("monomind init is not available here")
	}
	root := env.ProfileRoot(env.profileID())
	if root == "" {
		return fmt.Errorf("profile %q has no usable folder", env.profileID())
	}
	return env.InitMonomindProfile(ctx, root, progress)
}
