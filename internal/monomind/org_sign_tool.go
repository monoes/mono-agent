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
	if kind := shimKind(found); kind != "" {
		if path, err = resolveShim(ctx, kind, root); err != nil {
			return nil, err
		}
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

// resolveShim is `<mise|asdf> which monomind` run in root: the binary the
// shim would run there, as an absolute, executable path.
func resolveShim(ctx context.Context, kind, root string) (string, error) {
	tool, err := exec.LookPath(kind)
	if err != nil {
		return "", fmt.Errorf("monomind is a %s shim, and %s is not on PATH to resolve it: %w", kind, kind, err)
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, tool, "which", "monomind")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s which monomind in %s: %w", kind, root, err)
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s which monomind in %s gave %q, not an absolute path", kind, root, path)
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return "", fmt.Errorf("%s which monomind in %s gave %s, which is not a file", kind, root, path)
	}
	return path, nil
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
