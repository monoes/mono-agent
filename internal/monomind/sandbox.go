package monomind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Agent turn sandboxing. Every name monomind defines is kept here, so a
// rename on the monomind side is a one-file change.
//
// Two paths exist. The real one is `agent exec --sandbox <mode>`, which
// monomind is adding (monomind#396) and advertises as CapAgentExecSandbox.
// Until then, monomind >= SandboxEnvMinVersion (2.11.1) sandboxes codex and
// grok when the turn's own env carries MONOMIND_GIT_LEVEL below "push": it
// is how orgs confine a role, read by the runner from `agent exec --env`
// (the caller's process env is not passed through). codex then runs with
// `--sandbox workspace-write` plus network, grok with `--sandbox
// workspace`. Other runtimes (copilot, qwen, antigravity, …) have no such
// path and stay unsandboxed until #396.
const (
	// CapAgentExecSandbox is advertised by a monomind whose agent exec
	// accepts SandboxFlag.
	CapAgentExecSandbox = "agent-exec-sandbox"
	// SandboxFlag is the agent exec flag that picks the sandbox mode.
	SandboxFlag = "--sandbox"

	// Sandbox modes (monomind#396).
	SandboxReadOnly       = "read-only"       // reads anywhere, writes nothing but temp
	SandboxWorkspaceWrite = "workspace-write" // writes the --cwd and temp dir, network on
	SandboxFull           = "full"            // no sandbox: today's default

	// SandboxEnvMinVersion is the first monomind whose codex and grok
	// runners read MONOMIND_GIT_LEVEL from the turn env (monomind 31ec5b2).
	SandboxEnvMinVersion = "2.11.1"
	// sandboxEnvLevel is the MONOMIND_GIT_LEVEL that sandboxes codex and
	// grok. "read" also keeps the runner's git guard at read-only.
	sandboxEnvLevel = "MONOMIND_GIT_LEVEL=read"
)

// TurnSandboxMode is the sandbox every agent turn except coder mode asks
// for (ExecOptions.Sandbox at each call site). Change it here to change
// them all.
var TurnSandboxMode = SandboxWorkspaceWrite

// sandboxEnvRuntimes are the runtimes the MONOMIND_GIT_LEVEL path covers.
var sandboxEnvRuntimes = map[string]bool{"codex": true, "grok": true}

// The sandbox a turn actually had, as TurnResult.SandboxStatus and the
// start event's sandbox_status report it. "" means the turn asked for none
// (coder mode).
const (
	SandboxStatusSandboxed        = "sandboxed"         // ran in the runtime's sandbox
	SandboxStatusScoped           = "scoped"            // claude: --access scoped restricts it (no monomind sandbox yet)
	SandboxStatusUnsupported      = "unsupported"       // monomind has --sandbox, but this runtime can't honour it
	SandboxStatusAwaitingMonomind = "awaiting-monomind" // this runtime needs monomind#396; ran without a sandbox
	SandboxStatusNeedsMonomind    = "needs-monomind"    // monomind older than SandboxEnvMinVersion; ran as before
	SandboxStatusOff              = "off"               // the mode was full, or monomind reported it off
)

// SandboxFields are the start event's sandbox report. Sandbox and
// SandboxUnsupported come from monomind (after #396); SandboxStatus is
// added by Exec (never by monomind) so that every reader of the stream —
// the chat journal, `chat` stdout, the desktop app — sees the same
// verdict, including the ones no monomind event can carry.
type SandboxFields struct {
	Sandbox            string `json:"sandbox,omitempty"`
	SandboxUnsupported bool   `json:"sandbox_unsupported,omitempty"`
	SandboxStatus      string `json:"sandbox_status,omitempty"`
	// NativeSandbox is who confines the turn's native tools, as ScanEntry
	// reports it per runtime: "monomind", a vendor sandbox mode or "none".
	NativeSandbox string `json:"native_sandbox,omitempty"`
}

// SandboxArgs is the single place that decides how a turn on runtime gets
// sandbox mode. It returns the agent exec args to add and the effective
// status. caps is the handshake (nil when it failed). modes is the
// runtime's `agent scan --json` sandbox_modes, nil when unknown. An empty
// mode asks for nothing: no args, status "".
//
// With CapAgentExecSandbox the flag is passed ONLY for a mode the runtime
// lists: monomind 2.19.0 refuses any other mode with a fatal
// "--sandbox <mode> is not supported by runtime" error, so passing it
// blindly failed every claude, copilot, antigravity, … turn. A runtime
// that lists only "full" runs without a sandbox (claude stays on its
// scoped access) and the turn says so. With modes unknown (the scan
// failed) no flag is ever passed; codex and grok fall back to the env
// path, which still works.
func SandboxArgs(caps *CapabilitySet, modes []string, runtime, mode string) (args []string, effective string) {
	switch {
	case mode == "":
		return nil, ""
	case caps.Has(CapAgentExecSandbox) && modes != nil:
		switch {
		case !slices.Contains(modes, mode):
			if mode == SandboxFull {
				return nil, SandboxStatusOff
			}
			if runtime == "claude" {
				return nil, SandboxStatusScoped
			}
			return nil, SandboxStatusUnsupported
		case mode == SandboxFull:
			return []string{SandboxFlag, mode}, SandboxStatusOff
		}
		return []string{SandboxFlag, mode}, SandboxStatusSandboxed
	case mode == SandboxFull:
		return nil, SandboxStatusOff
	case runtime == "claude":
		return nil, SandboxStatusScoped
	case !sandboxEnvRuntimes[runtime]:
		if caps.Has(CapAgentExecSandbox) {
			return nil, SandboxStatusUnsupported
		}
		return nil, SandboxStatusAwaitingMonomind
	case caps == nil || !versionAtLeast(caps.Version, SandboxEnvMinVersion):
		return nil, SandboxStatusNeedsMonomind
	case mode != SandboxWorkspaceWrite:
		// The env path has one level; read-only needs #396.
		return nil, SandboxStatusAwaitingMonomind
	}
	return []string{"--env", sandboxEnvLevel}, SandboxStatusSandboxed
}

// sandboxModesTTL bounds how long a scan's sandbox_modes are reused: a
// runtime installed or upgraded meanwhile is picked up within it.
const sandboxModesTTL = 10 * time.Minute

// scanModes caches `agent scan --json` sandbox_modes per runtime, so a
// long-running process (daemon, bridge) scans once, not per turn.
var scanModes struct {
	sync.Mutex
	modes map[string][]string
	at    time.Time
}

// scanForSandbox is Scan, swappable in tests.
var scanForSandbox = Scan

// SandboxModesFor returns runtime's sandbox_modes from `agent scan --json`,
// nil when the scan fails or the runtime isn't listed (callers then pass
// no --sandbox flag).
func SandboxModesFor(ctx context.Context, runtime string) []string {
	scanModes.Lock()
	defer scanModes.Unlock()
	if scanModes.modes == nil || time.Since(scanModes.at) > sandboxModesTTL {
		res, err := scanForSandbox(ctx)
		if err != nil || res == nil {
			return nil
		}
		m := map[string][]string{}
		for _, a := range res.Agents {
			if a.SandboxModes != nil {
				m[a.ID] = a.SandboxModes
			}
		}
		scanModes.modes, scanModes.at = m, time.Now()
	}
	return scanModes.modes[runtime]
}

// resetSandboxModes clears the cache (tests).
func resetSandboxModes() {
	scanModes.Lock()
	scanModes.modes = nil
	scanModes.Unlock()
}

// sandboxStatus refines the adapter's verdict with the start event: once
// monomind reports the sandbox itself (#396), its report wins.
func sandboxStatus(effective string, start SandboxFields) string {
	switch {
	case effective == "":
		return ""
	case start.SandboxUnsupported:
		return SandboxStatusUnsupported
	case start.Sandbox == SandboxFull || start.Sandbox == "off":
		return SandboxStatusOff
	case start.Sandbox != "":
		return SandboxStatusSandboxed
	}
	return effective
}

// SandboxWorkspaceDir returns ~/.monoagent/workspaces/<purpose>, creating
// it: the empty folder a sandboxed turn with no natural folder of its own
// runs in, so workspace-write has a real, harmless workspace instead of
// whatever directory launched the process. One fixed folder per purpose,
// not a temp one per turn, because agent CLIs keep per-folder session
// state that would otherwise pile up (see orgdecide's deciderDir).
func SandboxWorkspaceDir(purpose string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".monoagent", "workspaces", purpose)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}

// Sandbox workspace purposes (SandboxWorkspaceDir) for the turns that have
// no folder of their own.
const (
	WorkspaceChat         = "chat"
	WorkspaceAgentAsk     = "agent-ask"
	WorkspaceTextHelper   = "text-helper"
	WorkspaceSummary      = "summary"
	WorkspaceRecordReview = "record-analyze"
	WorkspaceMatching     = "matching"
	WorkspaceAgentTest    = "agent-test"
)

// binCaps caches handshakes by binary path, for callers that already hold
// a path (most Exec callers got theirs from Ensure, which fills it).
var binCaps struct {
	sync.Mutex
	sets map[string]*CapabilitySet
	at   map[string]time.Time
}

func rememberCapabilities(bin string, vi *VersionInfo) {
	set := NewCapabilitySet(vi.Version, vi.Capabilities...)
	binCaps.Lock()
	defer binCaps.Unlock()
	if binCaps.sets == nil {
		binCaps.sets, binCaps.at = map[string]*CapabilitySet{}, map[string]time.Time{}
	}
	binCaps.sets[bin], binCaps.at[bin] = set, time.Now()
}

func capabilitiesFor(ctx context.Context, bin string) (*CapabilitySet, error) {
	if bin == "" {
		return Capabilities(ctx)
	}
	binCaps.Lock()
	set, at := binCaps.sets[bin], binCaps.at[bin]
	binCaps.Unlock()
	if set != nil && time.Since(at) < capabilityTTL {
		return set, nil
	}
	vi, err := Handshake(ctx, bin) // fills binCaps
	if err != nil {
		return nil, err
	}
	return NewCapabilitySet(vi.Version, vi.Capabilities...), nil
}
