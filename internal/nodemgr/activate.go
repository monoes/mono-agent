package nodemgr

import (
	"context"
	"os"

	"github.com/monoes/mono-agent/internal/shellpath"
)

// Activate puts the managed Node (and the npm-global bin it installs
// packages into) on this process's PATH, so every child — monomind, npm,
// agent CLIs with a `#!/usr/bin/env node` shebang — can use it.
//
// It costs a couple of stats when no managed Node is installed. When
// one is, a suitable system Node still wins: the managed bin is then only
// appended (so packages installed through it stay reachable) instead of
// prepended.
func Activate(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	m := New()
	npmBin := m.NpmBinDir()
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
	shellpath.Prepend(npmBin)
	shellpath.Prepend(m.BinDir(version))
}

func appendPath(dir string) {
	cur := os.Getenv("PATH")
	_ = os.Setenv("PATH", shellpath.Merge(cur, dir))
}
