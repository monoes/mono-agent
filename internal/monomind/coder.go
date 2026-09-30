package monomind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Exec access levels (monomind#355).
const (
	AccessScoped = "scoped"
	AccessFull   = "full"
	// AccessRead reads files, searches, uses the web and read-only shell
	// commands; no edits (agent-exec-access-read, monomind#388).
	AccessRead = "read"
)

// CoderSettings is the setting sources a coder turn loads: the user's own
// setup plus the working folder's (monomind#356). On a runtime other than
// claude it means "don't isolate the CLI's own config".
var CoderSettings = []string{"user", "project", "local"}

// DefaultCoderRuntime is the runtime a coder chat uses when none is named,
// and the only one an older monomind (without CapAgentExecFullAccessAny)
// runs with full access.
const DefaultCoderRuntime = "claude"

// CoderRuntime is one runtime's coder-mode support, as `coder status`
// reports it. Ready means a coder chat can run on it now: installed, full
// access supported, and monomind has every CoderCapabilities entry.
type CoderRuntime struct {
	ID           string `json:"id"`
	Installed    bool   `json:"installed"`
	FullAccess   bool   `json:"fullAccess"`
	Ready        bool   `json:"ready"`
	ToolActivity string `json:"toolActivity"` // full | start-only | none
	Resume       bool   `json:"resume"`
	Effort       bool   `json:"effort"`
	MaxTurns     bool   `json:"maxTurns"`
	ReportsCost  bool   `json:"reportsCost"`
	InitTarget   string `json:"initTarget"` // "" when the runtime has none (AGENTS.md only)
}

// CoderRuntimes derives each scanned runtime's coder support. Without
// CapAgentExecFullAccessAny monomind runs only claude with full access, and
// its scan predates the resume/effort/max_turns/reports_cost/init_target
// fields, so claude's are filled in with what that monomind did support.
// A nil or empty scan yields claude alone, not installed.
func CoderRuntimes(scan *ScanResult, caps *CapabilitySet) []CoderRuntime {
	capsReady := len(MissingCoderCapabilities(caps)) == 0
	anyRuntime := caps.Has(CapAgentExecFullAccessAny)
	var entries []ScanEntry
	if scan != nil {
		entries = scan.Agents
	}
	if len(entries) == 0 {
		// Nothing scanned: assume only what every coder-capable monomind
		// supports, claude with full access.
		entries, anyRuntime = []ScanEntry{{ID: DefaultCoderRuntime}}, false
	}
	out := make([]CoderRuntime, 0, len(entries))
	for _, e := range entries {
		r := CoderRuntime{
			ID: e.ID, Installed: e.Installed, FullAccess: e.FullAccess,
			ToolActivity: e.ToolActivityFidelity, Resume: e.Resume, Effort: e.Effort,
			MaxTurns: e.MaxTurns, ReportsCost: e.ReportsCost,
		}
		if e.InitTarget != nil {
			r.InitTarget = *e.InitTarget
		} else if e.ID == DefaultCoderRuntime {
			// claude's setup is claude's own; only other runtimes go
			// without one.
			r.InitTarget = DefaultCoderRuntime
		}
		if !anyRuntime {
			r.FullAccess = e.ID == DefaultCoderRuntime
			if r.FullAccess {
				r.Resume, r.Effort, r.MaxTurns, r.ReportsCost, r.InitTarget = true, true, true, true, DefaultCoderRuntime
				if r.ToolActivity == "" {
					r.ToolActivity = "full"
				}
			}
		}
		if r.ToolActivity == "" {
			r.ToolActivity = "none"
		}
		r.Ready = r.Installed && r.FullAccess && capsReady
		out = append(out, r)
	}
	return out
}

// FindCoderRuntime returns id's entry in list, nil when absent.
func FindCoderRuntime(list []CoderRuntime, id string) *CoderRuntime {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// CoderCapabilities is what coder mode needs from monomind.
var CoderCapabilities = []string{
	CapAgentExecFullAccess,
	CapAgentExecSettings,
	CapAgentExecToolActivity,
	CapInitJSON,
}

// MissingCoderCapabilities returns the coder capabilities set lacks, in
// CoderCapabilities order. A nil set lacks all of them.
func MissingCoderCapabilities(set *CapabilitySet) []string {
	var missing []string
	for _, c := range CoderCapabilities {
		if !set.Has(c) {
			missing = append(missing, c)
		}
	}
	return missing
}

// WorkspaceInit is `monomind init --json`'s result (monomind#358).
type WorkspaceInit struct {
	Root    string   `json:"root"`
	Created []string `json:"created"`
	Skipped []string `json:"skipped"`
}

// InitWorkspace sets dir up as a monomind project without touching any file
// already there (--if-missing), so it is safe on the user's own repos. The
// code graph is skipped (--no-graph) so a new chat is ready in seconds;
// monomind builds it on first use. Only the chat runtime's setup is added
// (--target, its CoderRuntime.InitTarget): the folder may be the user's own
// repo. Target "" is a runtime monomind has no init target for (an older
// monomind's pi, grok, …): the folder gets a minimal AGENTS.md and nothing
// Claude-specific, without running monomind.
func InitWorkspace(ctx context.Context, bin, dir, target string) (*WorkspaceInit, error) {
	if target == "" {
		return writeAgentsMD(dir)
	}
	if bin == "" {
		var err error
		if bin, err = Find(); err != nil {
			return nil, err
		}
	}
	if err := CheckOutside(bin, dir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, InitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "init", "--project", dir, "--if-missing", "--json", "--no-graph", "--target", target, "--yes", "--no-watch", "--no-install")
	cmd.Dir = dir
	cmd.Env = PinEnv(append(FilteredEnviron(), "CI=true"), bin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// --json reports a failure as {"success":false,"error":…} on stdout.
		var failed struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(lastJSONLine(out), &failed) == nil && failed.Error != "" {
			return nil, fmt.Errorf("monomind init %s: %s", dir, failed.Error)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("monomind init %s: %s", dir, msg)
	}
	var res WorkspaceInit
	if err := json.Unmarshal(lastJSONLine(out), &res); err != nil {
		return nil, fmt.Errorf("monomind init %s: unreadable result: %w", dir, err)
	}
	return &res, nil
}

// fallbackAgentsMD is the AGENTS.md written for a runtime monomind has no
// init target for; `monomind init --target agents` writes a fuller one.
const fallbackAgentsMD = `# AGENTS.md

Instructions for AI coding agents working in this folder.

- Keep changes focused on the task you were given.
- Never hardcode secrets or commit .env files.
- When the monomind MCP server is available (tools named mcp__monomind__*),
  use it for code navigation, impact analysis and persistent memory.
`

// writeAgentsMD creates dir/AGENTS.md unless one exists, reporting it the
// way monomind init --json does.
func writeAgentsMD(dir string) (*WorkspaceInit, error) {
	res := &WorkspaceInit{Root: dir, Created: []string{}, Skipped: []string{}}
	f, err := os.OpenFile(filepath.Join(dir, "AGENTS.md"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		res.Skipped = append(res.Skipped, "AGENTS.md")
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("writing AGENTS.md in %s: %w", dir, err)
	}
	_, err = f.WriteString(fallbackAgentsMD)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("writing AGENTS.md in %s: %w", dir, err)
	}
	res.Created = append(res.Created, "AGENTS.md")
	return res, nil
}

// lastJSONLine returns the last line of out that looks like a JSON object,
// tolerating progress text before it.
func lastJSONLine(out []byte) []byte {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") {
			return []byte(l)
		}
	}
	return out
}

// IsRoot reports whether this process runs as root, which full access
// refuses (monomind#355).
func IsRoot() bool { return os.Geteuid() == 0 }
