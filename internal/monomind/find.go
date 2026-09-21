package monomind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/shellpath"
)

// EnvOverride is the env var that pins the monomind binary path, checked
// before every discovery step.
const EnvOverride = "MONOMIND_BIN"

// ErrNotFound is returned when no usable monomind exists on the host. Its
// message is the actionable install hint users see in the UI and CLI.
type ErrNotFound struct {
	Tried []string
}

func (e *ErrNotFound) Error() string {
	return "monomind not found (AI engine) — install it with `npm install -g @monoes/monomindcli` " +
		"or set " + EnvOverride + " to the binary; tried: " + strings.Join(e.Tried, ", ")
}

// CandidatePaths returns the discovery ladder (protocol plan §6): env
// override → PATH → npm global → well-known local install roots.
func CandidatePaths() []string {
	var cands []string
	if env := strings.TrimSpace(os.Getenv(EnvOverride)); env != "" {
		cands = append(cands, env)
	}
	if path, err := exec.LookPath("monomind"); err == nil {
		cands = append(cands, path)
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands,
			filepath.Join(home, ".monoagent", "monomind-bundle", "bin", "monomind"),
			filepath.Join(home, ".npm-global", "bin", "monomind"),
			filepath.Join(home, ".local", "bin", "monomind"),
		)
		if runtime.GOOS == "windows" {
			cands = append(cands, filepath.Join(home, "AppData", "Roaming", "npm", "monomind.cmd"))
		} else {
			cands = append(cands, nvmCandidates(filepath.Join(home, ".nvm", "versions", "node"))...)
		}
	}
	if runtime.GOOS != "windows" {
		// `npm install -g @monoes/monomindcli` — the exact command this
		// app's own error message tells users to run — lands here whenever
		// npm's global prefix is Homebrew-managed (the default on a `brew
		// install node` setup): /opt/homebrew on Apple Silicon, /usr/local
		// on Intel Macs and commonly on Linux. exec.LookPath("monomind")
		// above already covers this for a process that inherited the
		// user's shell PATH, but a GUI app launched via Finder/Dock/open
		// does not — Homebrew's PATH additions come from shell rc files a
		// non-interactive, non-login process never sources — so a binary
		// that is genuinely installed and on the terminal's PATH can still
		// be invisible to the running app without these as explicit
		// fallback candidates, the same way ~/.npm-global/bin and
		// ~/.local/bin above already are.
		cands = append(cands, "/opt/homebrew/bin/monomind", "/usr/local/bin/monomind")
	}
	return cands
}

// nvmCandidates returns <root>/<version>/bin/monomind for every installed
// nvm Node version, newest first. A GUI app never has nvm's bin dir on PATH
// (nvm is wired up in .zshrc/.bashrc), so these must be probed directly.
func nvmCandidates(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		return versionAtLeast(versions[i], versions[j]) && versions[i] != versions[j]
	})
	cands := make([]string, 0, len(versions))
	for _, v := range versions {
		cands = append(cands, filepath.Join(root, v, "bin", "monomind"))
	}
	return cands
}

// Find locates an executable monomind binary. The returned path is absolute
// and verified executable.
//
// The binary's directory is also put on this process's PATH: monomind is a
// `#!/usr/bin/env node` script, and for nvm/Homebrew installs `node` sits
// right next to it. Without this, a GUI-launched app finds monomind but
// every exec of it dies with exit status 127 (env: node: not found).
func Find() (string, error) {
	var tried []string
	for _, cand := range CandidatePaths() {
		tried = append(tried, cand)
		if path, err := exec.LookPath(cand); err == nil {
			abs, err := filepath.Abs(path)
			if err != nil {
				abs = path
			}
			shellpath.Prepend(filepath.Dir(abs))
			return abs, nil
		}
	}
	return "", &ErrNotFound{Tried: tried}
}

// Handshake runs `monomind --version --json` and validates the payload
// against this client's requirements (protocol §2).
func Handshake(ctx context.Context, bin string) (*VersionInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "--version", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("handshake with %s failed: %w", bin, err)
	}
	var vi VersionInfo
	if err := json.Unmarshal(out, &vi); err != nil {
		return nil, fmt.Errorf("handshake: %s did not speak the protocol (unparseable --version --json): %w", bin, err)
	}
	if vi.V != ProtocolVersion {
		return nil, fmt.Errorf("handshake: protocol v%d, client implements v%d — update monomind or mono-agent", vi.V, ProtocolVersion)
	}
	if !versionAtLeast(vi.Version, MinMonomindVersion) {
		return nil, fmt.Errorf("monomind %s is too old (need >= %s): run `npm install -g @monoes/monomindcli@latest`", vi.Version, MinMonomindVersion)
	}
	for _, cap := range RequiredCapabilities {
		if !vi.HasCapability(cap) {
			return nil, fmt.Errorf("monomind %s lacks capability %q — update it: `npm install -g @monoes/monomindcli@latest`", vi.Version, cap)
		}
	}
	return &vi, nil
}

// Ensure is Find + Handshake in one call — the standard preamble for every
// monomind-backed command. Returns the binary path and handshake payload.
func Ensure(ctx context.Context) (string, *VersionInfo, error) {
	bin, err := Find()
	if err != nil {
		return "", nil, err
	}
	vi, err := Handshake(ctx, bin)
	if err != nil {
		return "", nil, err
	}
	return bin, vi, nil
}

// versionAtLeast is a tolerant semver-minimum check ("2.10.0" >= "2.9.25").
// Non-numeric suffixes (pre-release tags) sort below the plain version.
func versionAtLeast(got, min string) bool {
	parse := func(s string) []int {
		s = strings.TrimPrefix(strings.TrimSpace(s), "v")
		if i := strings.IndexAny(s, "-+"); i >= 0 {
			s = s[:i] // pre-release/build metadata: compare the numeric core
		}
		parts := strings.SplitN(s, ".", 3)
		nums := make([]int, len(parts))
		for i, p := range parts {
			n := 0
			for _, ch := range p {
				if ch < '0' || ch > '9' {
					break
				}
				n = n*10 + int(ch-'0')
			}
			nums[i] = n
		}
		return nums
	}
	g, m := parse(got), parse(min)
	for i := 0; i < len(g) || i < len(m); i++ {
		var gv, mv int
		if i < len(g) {
			gv = g[i]
		}
		if i < len(m) {
			mv = m[i]
		}
		if gv != mv {
			return gv > mv
		}
	}
	return true
}

// IsNotFound reports whether err is the actionable "monomind missing" error.
func IsNotFound(err error) bool {
	var nf *ErrNotFound
	return errors.As(err, &nf)
}
