package monomind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Capabilities monomind 2.22 declares for `org sign` (#568). Each feature
// is used only when the monomind that signs in the project advertises it;
// a monomind without it is an older binary, and mono-agent fails closed.
const (
	// CapOrgSignExpectHash: `org sign --expect-hash <hex>` refuses content
	// whose signable hash differs, reading the files once.
	CapOrgSignExpectHash = "org-sign-expect-hash"
	// CapOrgSignReviewJSON: `org sign <org> --format json` (no --yes) is the
	// review plus the hash of exactly what it reviewed.
	CapOrgSignReviewJSON = "org-sign-review-json"
	// CapOrgSignCheck: `org sign --check --format json`.
	CapOrgSignCheck = "org-sign-check"
)

// signTool is the monomind that signs in one project root, pinned: the
// binary Find resolves, and through a version shim (mise, asdf) the one
// the shim picks in that root, as an absolute path. Its handshake runs in
// the root with this process's environment, the same as the sign, so the
// capabilities are the signing binary's own.
type signTool struct {
	path    string
	version string
	caps    map[string]bool
	at      time.Time

	helpOnce sync.Once
	help     string
}

func (t *signTool) has(cap string) bool { return t != nil && t.caps[cap] }

// orgSignHelp is `org sign --help` of the pinned binary, run in root.
func (t *signTool) orgSignHelp(ctx context.Context, root string) string {
	t.helpOnce.Do(func() {
		cctx, cancel := context.WithTimeout(ctx, orgTimeout)
		defer cancel()
		cmd := exec.CommandContext(cctx, t.path, "org", "sign", "--help")
		cmd.Dir = root
		out, _ := cmd.CombinedOutput()
		t.help = string(out)
	})
	return t.help
}

var signTools struct {
	sync.Mutex
	byKey map[string]*signTool // Find()'s path + "\x00" + root
}

// signToolFor pins the monomind that signs in root (cached per found path
// and root, for capabilityTTL). An error means it can't be told which
// binary that is or what it can do: callers fail closed.
func signToolFor(ctx context.Context, root string) (*signTool, error) {
	found, err := Find()
	if err != nil {
		return nil, err
	}
	key := found + "\x00" + root
	signTools.Lock()
	defer signTools.Unlock()
	if t := signTools.byKey[key]; t != nil && time.Since(t.at) < capabilityTTL {
		return t, nil
	}
	path := found
	kind := shimKind(found)
	if kind != "" {
		if path, err = resolveSignShim(ctx, kind); err != nil {
			return nil, err
		}
	}
	if path, err = trustedSignBinary(path, kind, root); err != nil {
		return nil, err
	}
	t := &signTool{path: path, at: time.Now()}
	t.version, t.caps, err = handshakeIn(ctx, path, root)
	if err != nil {
		return nil, err
	}
	if signTools.byKey == nil {
		signTools.byKey = map[string]*signTool{}
	}
	signTools.byKey[key] = t
	return t, nil
}

// shimKind names the version manager whose shim bin is ("" when none):
// a shim is a stand-in that picks the real binary per directory.
func shimKind(bin string) string {
	p := filepath.ToSlash(bin)
	switch {
	case strings.Contains(p, "/mise/shims/"), strings.Contains(p, "/rtx/shims/"):
		return "mise"
	case strings.Contains(p, "/.asdf/shims/"), strings.Contains(p, "/asdf/shims/"):
		return "asdf"
	}
	return ""
}

// resolveSignShim is `<mise|asdf> which monomind`: the binary the shim picks,
// as an absolute path. It runs in the home directory, never the project
// root: a role can plant a `.tool-versions` or `mise.toml` there that
// points the manager at a binary it wrote (#295 review; the general fix
// for every project-dir call is #301).
func resolveSignShim(ctx context.Context, kind string) (string, error) {
	tool, err := exec.LookPath(kind)
	if err != nil {
		return "", fmt.Errorf("monomind is a %s shim, and %s is not on PATH to resolve it: %w", kind, kind, err)
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, tool, "which", "monomind")
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving the %s shim needs a home directory: %w", kind, err)
	}
	cmd.Dir = home
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s which monomind: %w", kind, err)
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s which monomind gave %q, not an absolute path", kind, path)
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return "", fmt.Errorf("%s which monomind gave %s, which is not a file", kind, path)
	}
	return path, nil
}

// managerInstalls is where a version manager keeps what it installs:
// mise's $MISE_DATA_DIR/installs (default ~/.local/share/mise/installs),
// asdf's $ASDF_DATA_DIR/installs (default ~/.asdf/installs).
func managerInstalls(kind string) string {
	home, _ := os.UserHomeDir()
	switch kind {
	case "mise":
		if d := os.Getenv("MISE_DATA_DIR"); d != "" {
			return filepath.Join(d, "installs")
		}
		return filepath.Join(home, ".local", "share", "mise", "installs")
	case "asdf":
		if d := os.Getenv("ASDF_DATA_DIR"); d != "" {
			return filepath.Join(d, "installs")
		}
		return filepath.Join(home, ".asdf", "installs")
	}
	return ""
}

// trustedSignBinary is the real path of the monomind that may sign in
// root, or an error (fail closed: nothing signs, nothing counts as
// enforcing). It must lie outside the project — a role can write there —
// and, when a version manager picked it, inside that manager's installs.
func trustedSignBinary(path, kind, root string) (string, error) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving monomind %s: %w", path, err)
	}
	if under(realDir(root), real) {
		return "", fmt.Errorf("monomind resolves to %s, inside the project %s, where a role could have written it — not signing with it", real, root)
	}
	if kind != "" {
		installs := realDir(managerInstalls(kind))
		if installs == "" || !under(installs, real) {
			return "", fmt.Errorf("the %s shim resolves monomind to %s, outside %s's installs (%s) — not signing with it", kind, real, kind, installs)
		}
	}
	return real, nil
}

// realDir is dir's real path, else its absolute one ("" for "").
func realDir(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// under reports whether target is dir or inside it.
func under(dir, target string) bool {
	if dir == target {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(dir, sep) {
		dir += sep
	}
	return strings.HasPrefix(target, dir)
}

// handshakeIn is `<bin> --version --json` run in root: the version and
// the capabilities, from a top-level `capabilities` array or one under
// `org` (the exact place for the org-sign ones is to be confirmed).
func handshakeIn(ctx context.Context, bin, root string) (string, map[string]bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "--version", "--json")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", nil, fmt.Errorf("handshake with %s in %s: %w", bin, root, err)
	}
	var vi struct {
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
		Org          *struct {
			Capabilities []string `json:"capabilities"`
		} `json:"org"`
	}
	if err := json.Unmarshal(JSONBody(out), &vi); err != nil {
		return "", nil, fmt.Errorf("handshake with %s in %s: %w", bin, root, err)
	}
	if vi.Version == "" {
		return "", nil, errors.New("handshake: no version")
	}
	caps := map[string]bool{}
	for _, c := range vi.Capabilities {
		caps[c] = true
	}
	if vi.Org != nil {
		for _, c := range vi.Org.Capabilities {
			caps[c] = true
		}
	}
	return vi.Version, caps, nil
}

// resetSignTools drops the pinned binaries (ResetCapabilityCache).
func resetSignTools() {
	signTools.Lock()
	signTools.byKey = nil
	signTools.Unlock()
}
