package monomind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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

// signTool is the monomind that signs in one project root: the binary
// Find pins (a version shim resolved from the home directory, never the
// root, to a binary under the manager's installs; pin.go), refused when it
// or its node lies inside the root. Its handshake, `org sign --help`, the
// check, the review and the sign all run through this one path, in the
// root, so the capabilities are the signing binary's own.
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
		cmd := CommandContext(cctx, t.path, "org", "sign", "--help")
		inRoot(cmd, root)
		out, _ := cmd.CombinedOutput()
		t.help = string(out)
	})
	return t.help
}

var signTools struct {
	sync.Mutex
	byKey map[string]*signTool // pinned path + "\x00" + root
}

// signToolFor is the monomind that signs in root (cached per pinned path
// and root, for capabilityTTL). An error means it can't be pinned or told
// what it can do: callers fail closed.
func signToolFor(ctx context.Context, root string) (*signTool, error) {
	path, err := findIn(root)
	if err != nil {
		return nil, err
	}
	key := path + "\x00" + root
	signTools.Lock()
	defer signTools.Unlock()
	if t := signTools.byKey[key]; t != nil && time.Since(t.at) < capabilityTTL {
		return t, nil
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

// handshakeIn is `<bin> --version --json` run in root: the version and
// the top-level `capabilities` array (monomind#578: the org-sign ones are
// there, and nothing under "org" counts).
func handshakeIn(ctx context.Context, bin, root string) (string, map[string]bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := CommandContext(cctx, bin, "--version", "--json")
	inRoot(cmd, root)
	out, err := cmd.Output()
	if err != nil {
		return "", nil, fmt.Errorf("handshake with %s in %s: %w", bin, root, err)
	}
	var vi struct {
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
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
	return vi.Version, caps, nil
}

// unknownOption reports whether a failed `org sign` is monomind's usage
// refusal of a flag it doesn't have (exit status 2, "unknown option"): the
// feature is unsupported, whatever the handshake advertised (#578).
func unknownOption(err error, out []byte) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)+string(ee.Stderr)), "unknown option")
}

// resetSignTools drops the pinned binaries (ResetCapabilityCache).
func resetSignTools() {
	signTools.Lock()
	signTools.byKey = nil
	signTools.Unlock()
}
