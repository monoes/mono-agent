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
// It is a no-op, costing one stat, when no managed Node is installed. When
// one is, a suitable system Node still wins: the managed bin is then only
// appended (so packages installed through it stay reachable) instead of
// prepended.
func Activate(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	m := New()
	version, ok := m.Current()
	if !ok {
		return
	}
	npmBin := m.NpmBinDir()
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
