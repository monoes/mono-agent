package health

import (
	"context"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Upkeep of what monomind keeps outside monoagent: the version its MCP
// server is pinned to in the profile folder, and the Claude Agent SDK it
// downloads on first use.
const (
	CheckMonomindMCPPin = "monomind.mcp_pin"
	CheckMonomindDeps   = "monomind.deps"

	FixMonomindRepin = "monomind.repin" // confirm: `monomind init --force` in the profile folder
	FixMonomindDeps  = "monomind.deps"  // confirm: `monomind deps install`
)

func monomindUpkeepChecks() []Check {
	return []Check{
		{ID: CheckMonomindMCPPin, Group: GroupMonomind, Title: "monomind MCP version", Features: []string{"orgs", "memory", "knowledge graph"},
			DependsOn: []string{CheckMonomindProfileInit, CheckMonomindHandshake}, Timeout: 30 * time.Second, Run: checkMonomindMCPPin},
		{ID: CheckMonomindDeps, Group: GroupMonomind, Title: "Claude Agent SDK", Features: []string{"agent chat", "AI agents"},
			DependsOn: []string{CheckMonomindHandshake}, Run: checkMonomindDeps},
	}
}

func monomindUpkeepFixes() []Fix {
	return []Fix{
		{FixInfo: FixInfo{ID: FixMonomindRepin, Label: "Pin this profile's monomind MCP server to the installed version", Safety: SafetyConfirm,
			Command: "monomind init --force --yes --no-watch --no-install in the profile folder (refreshes monomind's own files and the " +
				".mcp.json version pin; files you edited are kept, with monomind's version beside them as <file>.monomind-new)"},
			Apply: fixMonomindRepin},
		{FixInfo: FixInfo{ID: FixMonomindDeps, Label: "Download the Claude Agent SDK (about 300 MB)", Safety: SafetyConfirm,
			Command: "monomind deps install", Optional: true}, Apply: fixMonomindDeps},
	}
}

// checkMonomindMCPPin: since monomind 2.19 `init` pins .mcp.json to the
// version that ran it, and init only runs for a new profile, so after a
// monomind update Claude Code would keep starting the old server.
func checkMonomindMCPPin(ctx context.Context, env *Env) Result {
	if env.ProfileRoot == nil || env.MonomindHandshake == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	vi, err := env.MonomindHandshake(ctx)
	if err != nil {
		return Result{Status: StatusSkip, Summary: "handshake failed"}
	}
	root := env.ProfileRoot(env.profileID())
	if !monomind.IsInitializedAt(root) {
		return Result{Status: StatusSkip, Summary: "this profile's folder is not set up for monomind yet"}
	}
	pinned, stale := monomind.StaleMCPPin(root, vi.Version)
	if !stale {
		if pinned == "" {
			return Result{Status: StatusOK, Summary: "not pinned"}
		}
		return Result{Status: StatusOK, Summary: "pinned to v" + pinned}
	}
	return Result{Status: StatusWarn, Summary: fmt.Sprintf("this profile's .mcp.json pins monomind v%s, installed is v%s", pinned, vi.Version),
		Detail: "Claude Code starts the pinned version, so the MCP tools monomind added since are missing; monomind doctor warns that the running server predates the install",
		FixID:  FixMonomindRepin}
}

func fixMonomindRepin(ctx context.Context, env *Env, progress func(string)) error {
	if env.RepinMonomindProfile == nil || env.ProfileRoot == nil {
		return fmt.Errorf("monomind init is not available here")
	}
	root := env.ProfileRoot(env.profileID())
	if root == "" || !monomind.IsInitializedAt(root) {
		return fmt.Errorf("profile %q has no folder set up for monomind", env.profileID())
	}
	return env.RepinMonomindProfile(ctx, root, progress)
}

// checkMonomindDeps: the Claude Agent SDK installs on first use, which can
// outlast a chat turn's or `agent models`' deadline. Info, never a failure:
// without the Claude runtime nothing needs it.
func checkMonomindDeps(ctx context.Context, env *Env) Result {
	if env.MonomindHandshake == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	vi, err := env.MonomindHandshake(ctx)
	if err != nil {
		return Result{Status: StatusSkip, Summary: "handshake failed"}
	}
	if !monomind.DepsInstallSupported(vi.Version) {
		return Result{Status: StatusSkip, Summary: fmt.Sprintf("monomind %s installs it on first use (update to %s to install it ahead)", vi.Version, monomind.DepsInstallMinVersion)}
	}
	if env.Home == "" {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	if monomind.ClaudeSDKInstalled(env.Home) {
		return Result{Status: StatusOK, Summary: "installed"}
	}
	return Result{Status: StatusInfo, Summary: "not installed yet — monomind downloads it (about 300 MB) on the first Claude turn",
		Detail: "that first turn can outlast the deadline of a chat turn or of the model list; install it ahead if you use the Claude runtime",
		FixID:  FixMonomindDeps}
}

func fixMonomindDeps(ctx context.Context, env *Env, progress func(string)) error {
	if env.InstallMonomindDeps == nil {
		return fmt.Errorf("monomind deps install is not available here")
	}
	return env.InstallMonomindDeps(ctx, progress)
}
