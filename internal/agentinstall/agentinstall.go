// Package agentinstall installs AI agent runtimes (Claude Code, Codex, …)
// from the install hint monomind reports for each one in `agent scan`.
// monomind stays the single owner of the runtime list; this package only
// executes what it says, and only in the two shapes it can do safely:
// `npm install -g <pkgs>` and a vendor `curl -fsSL https://… | bash`
// installer. Anything else is shown to the user as a manual step.
package agentinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/nodemgr"
)

// Kind is how a runtime gets installed.
type Kind string

const (
	KindNpm    Kind = "npm"
	KindScript Kind = "script"
	KindManual Kind = "manual"
)

// Recipe is a parsed install hint.
type Recipe struct {
	Kind      Kind     `json:"kind"`
	Packages  []string `json:"packages,omitempty"`   // npm
	ScriptURL string   `json:"script_url,omitempty"` // script
	Shell     string   `json:"shell,omitempty"`      // script: bash | sh
	Hint      string   `json:"hint"`                 // the original text
}

var (
	npmPkg      = regexp.MustCompile(`^(@[a-z0-9][\w.-]*/)?[a-z0-9][\w.-]*(@[\w.^~<>=*-]+)?$`)
	curlInstall = regexp.MustCompile(`^curl\s+-fsSL\s+(https://\S+)\s*\|\s*(bash|sh)$`)
)

// Parse turns a hint into a recipe; unrecognized hints are KindManual.
func Parse(hint string) Recipe {
	hint = strings.TrimSpace(hint)
	r := Recipe{Kind: KindManual, Hint: hint}
	fields := strings.Fields(hint)
	if len(fields) >= 4 && fields[0] == "npm" && fields[1] == "install" && (fields[2] == "-g" || fields[2] == "--global") {
		for _, p := range fields[3:] {
			if !npmPkg.MatchString(p) {
				return r
			}
		}
		r.Kind, r.Packages = KindNpm, fields[3:]
		return r
	}
	if m := curlInstall.FindStringSubmatch(hint); m != nil && runtime.GOOS != "windows" {
		if u, err := url.Parse(m[1]); err == nil && u.Scheme == "https" && u.Host != "" {
			r.Kind, r.ScriptURL, r.Shell = KindScript, m[1], m[2]
		}
	}
	return r
}

// Installer runs recipes.
type Installer struct {
	Node *nodemgr.Manager
	HTTP *http.Client
}

// New returns an Installer using the managed-Node-aware npm.
func New() *Installer {
	return &Installer{Node: nodemgr.New(), HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

// ErrManual is returned for recipes that need the user.
var ErrManual = errors.New("this runtime can't be installed automatically")

// InstallTimeout bounds one install. Vendor installers can wait on a
// person (a prompt, sudo); run with no terminal they fail instead, and this
// is the backstop for anything that still hangs.
var InstallTimeout = 15 * time.Minute

// Install runs a recipe, streaming output to progress.
func (in *Installer) Install(ctx context.Context, r Recipe, progress func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, InstallTimeout)
	defer cancel()
	switch r.Kind {
	case KindNpm:
		_, err := in.Node.InstallGlobal(ctx, progress, r.Packages...)
		return err
	case KindScript:
		return in.runScript(ctx, r, progress)
	default:
		return fmt.Errorf("%w — %s", ErrManual, r.Hint)
	}
}

// runScript downloads the vendor installer with Go (no dependency on curl
// being installed), reports its size and SHA-256 so the log shows exactly
// what ran, and executes it with the named shell.
func (in *Installer) runScript(ctx context.Context, r Recipe, progress func(string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.ScriptURL, nil)
	if err != nil {
		return err
	}
	resp, err := in.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("downloading installer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading installer: %s: HTTP %d", r.ScriptURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	progress(fmt.Sprintf("downloaded installer %s (%d bytes, sha256 %s)", r.ScriptURL, len(body), hex.EncodeToString(sum[:])))

	f, err := os.CreateTemp("", "monoagent-installer-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	f.Close()

	progress("running installer with " + r.Shell)
	cmd := exec.CommandContext(ctx, r.Shell, f.Name())
	cmd.Stdin = nil // installers must not wait for input
	return nodemgr.StreamCmd(cmd, progress)
}
