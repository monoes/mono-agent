package monomind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Agent turn sandboxing. The names below follow the contract proposed to
// monomind (`agent exec --sandbox off|workspace`, capability
// agent-exec-sandbox, start event `sandbox` / `sandbox_unsupported`). They
// live only here so a rename on the monomind side is a one-file change.
const (
	// CapAgentExecSandbox is advertised by a monomind whose agent exec
	// accepts SandboxFlag.
	CapAgentExecSandbox = "agent-exec-sandbox"
	// SandboxFlag is the agent exec flag that picks the sandbox mode.
	SandboxFlag = "--sandbox"
	// SandboxOff is monomind's default: approvals and sandbox off.
	SandboxOff = "off"
	// SandboxWorkspace runs the turn in the runtime's own sandbox: it
	// writes the --cwd and the temp dir, reads elsewhere, network on.
	SandboxWorkspace = "workspace"
)

// The sandbox a finished (or running) turn actually had, as
// TurnResult.SandboxStatus and the start event's sandbox_status report it.
// "" means the turn never asked for one (coder mode).
const (
	SandboxStatusSandboxed     = "sandboxed"      // monomind ran it in the runtime's sandbox
	SandboxStatusUnsupported   = "unsupported"    // the runtime has no sandbox; it ran without one
	SandboxStatusNeedsMonomind = "needs-monomind" // monomind lacks CapAgentExecSandbox; ran as before
	SandboxStatusOff           = "off"            // asked for, but monomind reported it off
)

// SandboxFields are the start event's sandbox report. Sandbox and
// SandboxUnsupported come from monomind; SandboxStatus is added by Exec
// (never by monomind) so that every reader of the stream — the chat
// journal, `chat` stdout, the desktop app — sees the same verdict,
// including "needs-monomind", which no monomind event can carry.
type SandboxFields struct {
	Sandbox            string `json:"sandbox,omitempty"`
	SandboxUnsupported bool   `json:"sandbox_unsupported,omitempty"`
	SandboxStatus      string `json:"sandbox_status,omitempty"`
}

// sandboxStatus derives the verdict for a turn that requested a sandbox.
func sandboxStatus(requested string, supported bool, start SandboxFields) string {
	switch {
	case requested == "":
		return ""
	case !supported:
		return SandboxStatusNeedsMonomind
	case start.SandboxUnsupported:
		return SandboxStatusUnsupported
	case start.Sandbox != "" && start.Sandbox != SandboxOff:
		return SandboxStatusSandboxed
	case start.Sandbox == SandboxOff:
		return SandboxStatusOff
	}
	// The capability was advertised but the start event said nothing:
	// report what was passed rather than guess the runtime ignored it.
	return SandboxStatusSandboxed
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

// sandboxSupported reports whether the monomind at bin (discovery when
// "") advertises CapAgentExecSandbox. Any failure reads as "no": the turn
// then runs exactly as it did before sandboxing existed.
func sandboxSupported(ctx context.Context, bin string) bool {
	set, err := capabilitiesFor(ctx, bin)
	return err == nil && set.Has(CapAgentExecSandbox)
}

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
