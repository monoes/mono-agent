package nodemgr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/shellpath"
)

// UserPathEnv holds PATH as it was before Activate put the managed Node in
// front of it. Children inherit it, so a monoagentcli started by the GUI
// still knows the user's own order.
const UserPathEnv = "MONOAGENT_USER_PATH"

// Activate puts the managed Node (and the npm-global bin it installs
// packages into) on this process's PATH, so every child — monomind, npm,
// agent CLIs with a `#!/usr/bin/env node` shebang — can use it.
//
// It costs a couple of stats when no managed Node is installed. When
// one is, a suitable system Node still wins: the managed bin is then only
// appended (so packages installed through it stay reachable) instead of
// prepended. Only when the system Node is missing or too old does the
// managed one go first, and then it does for every child, the user's own
// commands in workflow exec nodes included; UserPathEnv, UserEnv and
// LookPathUser let such a child get the user's PATH back.
//
// It is safe to call again after an install, update or removal (the GUI
// does, so it needn't restart): versions no longer current are dropped
// from PATH first.
func Activate(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	m := New()
	npmBin := m.NpmBinDir()
	m.dropStale(npmBin)
	version, ok := m.Current()
	if !ok {
		// Packages may still have been installed into the private prefix
		// by a system npm whose own global prefix wasn't writable.
		if _, err := os.Stat(npmBin); err == nil {
			appendPath(npmBin)
		}
		return
	}
	if _, v, found := m.SystemNode(ctx); found && Suitable(v) {
		appendPath(m.BinDir(version))
		appendPath(npmBin)
		return
	}
	if os.Getenv(UserPathEnv) == "" {
		_ = os.Setenv(UserPathEnv, os.Getenv("PATH"))
	}
	shellpath.Prepend(npmBin)
	shellpath.Prepend(m.BinDir(version))
	// With the managed Node in use, npm's global prefix would otherwise be
	// the version folder itself, so `npm install -g` (monomind included)
	// would land there and `nodejs update` would delete it with the old
	// version. A prefix the user set is left alone.
	if os.Getenv("NPM_CONFIG_PREFIX") == "" && os.Getenv("npm_config_prefix") == "" {
		_ = os.Setenv("NPM_CONFIG_PREFIX", m.NpmRoot)
	}
}

// dropStale removes managed version folders (and a npm-global bin that no
// longer exists) from PATH, and the npm prefix Activate set when the
// managed Node is gone, so a re-run reflects what is installed now.
func (m *Manager) dropStale(npmBin string) {
	var keep []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if within(dir, m.Root) {
			continue
		}
		if dir == npmBin {
			if _, err := os.Stat(dir); err != nil {
				continue
			}
		}
		keep = append(keep, dir)
	}
	_ = os.Setenv("PATH", strings.Join(keep, string(os.PathListSeparator)))
	if _, ok := m.Current(); !ok && os.Getenv("NPM_CONFIG_PREFIX") == m.NpmRoot {
		_ = os.Unsetenv("NPM_CONFIG_PREFIX")
	}
}

func appendPath(dir string) {
	cur := os.Getenv("PATH")
	_ = os.Setenv("PATH", shellpath.Merge(cur, dir))
}

// UserPath is the user's PATH without the managed Node in front: the one
// Activate saved when it prepended, else the current PATH.
func UserPath() string {
	if p := os.Getenv(UserPathEnv); p != "" {
		return p
	}
	return os.Getenv("PATH")
}

// UserEnv returns env with PATH set to UserPath, for a child that runs the
// user's own commands (a workflow exec node) and should find the `node`
// they would find in their shell, not the managed one.
func UserEnv(env []string) []string {
	return withEnv(env, "PATH", UserPath())
}

// LookPathUser is exec.LookPath over UserPath. exec.Command resolves a bare
// name through this process's PATH whatever the child's env says, so a
// caller using UserEnv resolves the command with this too.
func LookPathUser(file string) (string, error) {
	if strings.ContainsAny(file, `/\`) {
		return exec.LookPath(file)
	}
	for _, dir := range filepath.SplitList(UserPath()) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		if p, err := exec.LookPath(filepath.Join(dir, file)); err == nil {
			return p, nil
		}
	}
	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}
