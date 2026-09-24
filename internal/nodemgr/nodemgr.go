// Package nodemgr downloads and manages a private Node.js runtime under
// ~/.monoagent/node, for machines without a suitable system Node. monomind
// and the npm-based agent runtimes need Node; with this, installing them no
// longer requires the user to install Node first
// (docs/plans/2026-09-24-setup-and-health-check.md §7).
//
// The managed Node is only ever used by processes monoagent starts: it is
// put on this process's PATH (see Activate) and never written to shell rc
// files. A suitable system Node always wins.
package nodemgr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
)

// MinVersion is the oldest Node monomind supports.
const MinVersion = "22.12.0"

// DefaultBaseURL is the official Node.js distribution server.
const DefaultBaseURL = "https://nodejs.org/dist"

// currentFile names the file holding the active managed version (a plain
// file rather than a symlink so it works the same on Windows).
const currentFile = "current"

// Manager manages Node versions under Root.
type Manager struct {
	Root    string // ~/.monoagent/node
	NpmRoot string // ~/.monoagent/npm-global — npm's global prefix for managed installs
	BaseURL string
	HTTP    *http.Client
	GOOS    string
	GOARCH  string

	// probe runs `<node> --version`; tests replace it to exercise another
	// OS's install flow (e.g. Windows' node.exe) on this machine.
	probe func(ctx context.Context, node string) (string, error)
	// Tests shrink the size caps, pin their own signing key and fake the
	// filesystem the musl check looks at.
	maxDownload, maxUnpacked int64
	keys                     openpgp.EntityList
	sysRoot                  string
}

func (m *Manager) nodeVersion(ctx context.Context, node string) (string, error) {
	if m.probe != nil {
		return m.probe(ctx, node)
	}
	return NodeVersion(ctx, node)
}

// New returns a Manager for the current user and platform.
func New() *Manager {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	data := filepath.Join(home, ".monoagent")
	return &Manager{
		Root:    filepath.Join(data, "node"),
		NpmRoot: filepath.Join(data, "npm-global"),
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
}

// Installed lists the installed managed versions, newest first.
func (m *Manager) Installed() ([]string, error) {
	entries, err := os.ReadDir(m.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && isVersion(e.Name()) {
			if _, err := os.Stat(m.NodePath(e.Name())); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return Compare(out[i], out[j]) > 0 })
	return out, nil
}

// Current returns the active managed version, if one is installed.
func (m *Manager) Current() (string, bool) {
	b, err := os.ReadFile(filepath.Join(m.Root, currentFile))
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(string(b))
	if !isVersion(v) {
		return "", false
	}
	if _, err := os.Stat(m.NodePath(v)); err != nil {
		return "", false
	}
	return v, true
}

// Use makes an installed version the active one.
func (m *Manager) Use(version string) error {
	version = normalize(version)
	if !isVersion(version) {
		return fmt.Errorf("%q is not a Node version (want x.y.z)", version)
	}
	unlock, err := m.lock(context.Background(), nil)
	if err != nil {
		return err
	}
	defer unlock()
	return m.use(version)
}

// use is Use for a caller already holding the lock.
func (m *Manager) use(version string) error {
	if _, err := os.Stat(m.NodePath(version)); err != nil {
		return fmt.Errorf("node %s is not installed", version)
	}
	return os.WriteFile(filepath.Join(m.Root, currentFile), []byte(version+"\n"), 0o644)
}

// Remove deletes one managed version, or everything (the runtime and the
// packages installed with it) when version is "". A version a running
// process uses is kept, with an *InUseError (see removeVersion for how far
// that detection reaches on each OS).
func (m *Manager) Remove(version string) error {
	if version == "" {
		return m.removeAll()
	}
	version = normalize(version)
	// The version names a folder under Root that is deleted whole, so it
	// must be exactly x.y.z: `remove ..` used to delete all of ~/.monoagent.
	if !isVersion(version) {
		return fmt.Errorf("%q is not a Node version (want x.y.z)", version)
	}
	if _, err := os.Stat(filepath.Join(m.Root, version)); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("node %s is not installed", version)
	}
	unlock, err := m.lock(context.Background(), nil)
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.removeVersion(version); err != nil {
		return err
	}
	if cur, err := os.ReadFile(filepath.Join(m.Root, currentFile)); err == nil && strings.TrimSpace(string(cur)) == version {
		_ = os.Remove(filepath.Join(m.Root, currentFile))
	}
	return nil
}

// removeAll deletes Root and NpmRoot, refusing while a process runs from
// either. The lock file goes last, while still held, so a process waiting
// for it notices and starts over on a new one (see lock).
func (m *Manager) removeAll() error {
	if _, err := os.Stat(m.Root); errors.Is(err, os.ErrNotExist) {
		return os.RemoveAll(m.NpmRoot)
	}
	unlock, err := m.lock(context.Background(), nil)
	if err != nil {
		return err
	}
	defer unlock()
	if pid, ok := processUsing(m.Root, m.NpmRoot); ok {
		return &InUseError{PID: pid}
	}
	versions, err := m.Installed()
	if err != nil {
		return err
	}
	for _, v := range versions {
		if err := m.removeVersion(v); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != lockFile {
			if err := os.RemoveAll(filepath.Join(m.Root, e.Name())); err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(m.NpmRoot); err != nil {
		return err
	}
	// Windows refuses to delete the open lock file; it and Root then stay.
	if os.Remove(filepath.Join(m.Root, lockFile)) == nil {
		_ = os.Remove(m.Root)
	}
	return nil
}

// BinDir is the directory holding node/npm for a version.
func (m *Manager) BinDir(version string) string {
	dir := filepath.Join(m.Root, normalize(version))
	if m.GOOS == "windows" {
		return dir
	}
	return filepath.Join(dir, "bin")
}

// NodePath is the node executable of a version.
func (m *Manager) NodePath(version string) string {
	if m.GOOS == "windows" {
		return filepath.Join(m.BinDir(version), "node.exe")
	}
	return filepath.Join(m.BinDir(version), "node")
}

// NpmPath is the npm executable of a version.
func (m *Manager) NpmPath(version string) string {
	if m.GOOS == "windows" {
		return filepath.Join(m.BinDir(version), "npm.cmd")
	}
	return filepath.Join(m.BinDir(version), "npm")
}

// NpmBinDir is where `npm install -g` through the managed Node puts
// executables.
func (m *Manager) NpmBinDir() string {
	if m.GOOS == "windows" {
		return m.NpmRoot
	}
	return filepath.Join(m.NpmRoot, "bin")
}

// NpmEnv returns the environment for running the managed npm: its prefix
// is the managed global folder and its node is first on PATH.
func (m *Manager) NpmEnv(version string) []string {
	env := os.Environ()
	path := m.BinDir(version) + string(os.PathListSeparator) + os.Getenv("PATH")
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, "PATH") || strings.EqualFold(k, "NPM_CONFIG_PREFIX") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+path, "NPM_CONFIG_PREFIX="+m.NpmRoot)
}

// SystemNode finds a Node on PATH that is not the managed one and reports
// its version (cached, see cachedNodeVersion). found is false when there
// is none.
func (m *Manager) SystemNode(ctx context.Context) (path, version string, found bool) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || within(dir, m.Root) {
			continue
		}
		name := "node"
		if m.GOOS == "windows" {
			name = "node.exe"
		}
		cand := filepath.Join(dir, name)
		if info, err := os.Stat(cand); err != nil || info.IsDir() {
			continue
		}
		v, err := m.cachedNodeVersion(ctx, cand)
		if err != nil {
			continue
		}
		return cand, v, true
	}
	return "", "", false
}

// NodeVersion runs `<node> --version`.
func NodeVersion(ctx context.Context, node string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "--version").Output()
	if err != nil {
		return "", err
	}
	v := normalize(strings.TrimSpace(string(out)))
	if !isVersion(v) {
		return "", fmt.Errorf("unexpected node --version output %q", out)
	}
	return v, nil
}

// Suitable reports whether a Node version is new enough for monomind.
func Suitable(version string) bool { return Compare(version, MinVersion) >= 0 }

// normalize strips a leading "v".
func normalize(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(normalize(v), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func isVersion(v string) bool { _, ok := parse(v); return ok }

// Compare orders two x.y.z versions (-1, 0, 1); unparseable sorts lowest.
func Compare(a, b string) int {
	pa, oka := parse(a)
	pb, okb := parse(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
