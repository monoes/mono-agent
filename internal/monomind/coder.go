package monomind

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Exec access levels (monomind#355).
const (
	AccessScoped = "scoped"
	AccessFull   = "full"
)

// CoderSettings is the setting sources a coder turn loads: the user's own
// Claude Code setup plus the working folder's (monomind#356).
var CoderSettings = []string{"user", "project", "local"}

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
// monomind builds it on first use.
func InitWorkspace(ctx context.Context, bin, dir string) (*WorkspaceInit, error) {
	if bin == "" {
		var err error
		if bin, err = Find(); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, InitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "init", "--project", dir, "--if-missing", "--json", "--no-graph", "--yes", "--no-watch", "--no-install")
	cmd.Dir = dir
	cmd.Env = append(FilteredEnviron(), "CI=true")
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
