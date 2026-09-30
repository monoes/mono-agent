package monomind

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// PinEnv returns env with PATH led by bin's pinned node dir and bin's own
// dir, so `#!/usr/bin/env node` (and any node monomind starts by name)
// resolves to the pinned interpreter whatever the working directory.
// Relative entries ("." or node_modules/.bin, which resolve against the
// project root) and version managers' shims dirs are dropped; the
// managers' global tool bin dirs go last instead (globalToolDirs), and the
// agent CLIs monomind starts are passed pinned (pinRuntimes). So nothing
// monomind runs by name in a project root is one the project picks.
func PinEnv(env []string, bin string) []string {
	dirs := []string{}
	if nd := nodeDirFor(bin); nd != "" {
		dirs = append(dirs, nd)
	}
	if filepath.IsAbs(bin) && (len(dirs) == 0 || dirs[0] != filepath.Dir(bin)) {
		dirs = append(dirs, filepath.Dir(bin))
	}
	out := make([]string, 0, len(env)+1)
	cur, key := "", "PATH"
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if k == "PATH" || (runtime.GOOS == "windows" && strings.EqualFold(k, "PATH")) {
			cur, key = v, k
			continue
		}
		out = append(out, kv)
	}
	shimKindOf := shimDirMatcher()
	var shims []shimDir
	parts := append([]string(nil), dirs...)
	for _, p := range filepath.SplitList(cur) {
		if !filepath.IsAbs(p) {
			continue
		}
		if kind := shimKindOf(p); kind != "" {
			shims = append(shims, shimDir{kind, p})
			continue
		}
		parts = appendNew(parts, p)
	}
	for _, p := range globalToolDirs(shims) {
		parts = appendNew(parts, p)
	}
	out = append(out, key+"="+strings.Join(parts, string(os.PathListSeparator)))
	return pinRuntimes(out, cur)
}

// PinEnvIn is PinEnv for a command that runs in roots: PATH entries and
// pinned agent CLIs inside them are dropped too (a direnv `PATH_add bin`,
// an override naming a project file), so the project can't supply them.
func PinEnvIn(env []string, bin string, roots ...string) []string {
	return scrubRoots(PinEnv(env, bin), roots...)
}

// inRoot sets cmd, built by Command/CommandContext, to run in root.
func inRoot(cmd *exec.Cmd, root string) {
	cmd.Dir = root
	cmd.Env = scrubRoots(cmd.Env, root)
}

func scrubRoots(env []string, roots ...string) []string {
	inside := func(p string) bool {
		if !filepath.IsAbs(p) {
			return false
		}
		for _, r := range roots {
			if r == "" {
				continue
			}
			rootAbs, err := filepath.Abs(r)
			if err != nil {
				rootAbs = r
			}
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				real = p
			}
			if within(p, rootAbs) || within(real, rootAbs) {
				return true
			}
		}
		return false
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case k == "PATH" || (runtime.GOOS == "windows" && strings.EqualFold(k, "PATH")):
			var keep []string
			for _, p := range filepath.SplitList(v) {
				if !inside(p) {
					keep = append(keep, p)
				}
			}
			kv = k + "=" + strings.Join(keep, string(os.PathListSeparator))
		case isRuntimeBinEnv(k) && inside(v):
			continue
		}
		out = append(out, kv)
	}
	return out
}

func appendNew(list []string, p string) []string {
	for _, q := range list {
		if q == p {
			return list
		}
	}
	return append(list, p)
}

type shimDir struct{ kind, dir string }

// shimDirMatcher names the version manager whose shims dir (or Volta's
// bin) a PATH entry is, or "": every configured one, and any mise/rtx/
// asdf/nodenv/proto shims dir by name.
func shimDirMatcher() func(dir string) string {
	known := map[string]string{}
	for _, kind := range shimKinds {
		for _, d := range shimDirs(kind) {
			known[filepath.Clean(d)] = kind
			if r, err := filepath.EvalSymlinks(d); err == nil {
				known[r] = kind
			}
		}
	}
	return func(dir string) string {
		c := filepath.Clean(dir)
		if k := known[c]; k != "" {
			return k
		}
		if r, err := filepath.EvalSymlinks(c); err == nil && known[r] != "" {
			return known[r]
		}
		s := filepath.ToSlash(c)
		for suffix, kind := range map[string]string{"/mise/shims": managerMise, "/rtx/shims": managerMise, "/asdf/shims": managerAsdf, "/.nodenv/shims": managerNodenv, "/.proto/shims": managerProto} {
			if strings.HasSuffix(s, suffix) {
				return kind
			}
		}
		return ""
	}
}

// globalToolDirs stands in for the shims dirs PinEnv drops: each
// manager's global tool bin dirs (the versions its shims pick outside any
// project), resolved from the neutral dir and each under its installs.
// When a manager can't list them, its shims dir goes last instead, after
// the system dirs, and a one-line notice is logged: tools keep working,
// with the residual risk that one not found anywhere else is still picked
// per project.
func globalToolDirs(shims []shimDir) []string {
	var bins, fallback []string
	done := map[string]bool{}
	for _, s := range shims {
		if done[s.kind] {
			continue
		}
		dirs, err := managerBinPaths(s.kind, s.dir)
		if err == nil {
			done[s.kind] = true
			bins = append(bins, dirs...)
			continue
		}
		noticeFallback(s.kind, err)
		fallback = append(fallback, s.dir)
	}
	return append(bins, fallback...)
}

var fallbackNotices sync.Map

func noticeFallback(kind string, err error) {
	if _, seen := fallbackNotices.LoadOrStore(kind, true); !seen {
		log.Printf("monomind: %s's global tool dirs are unavailable (%v); its shims dir stays last on PATH for monomind and its agents", kind, err)
	}
}

var binPaths struct {
	sync.Mutex
	byKind map[string]cachedBinPaths
}

type cachedBinPaths struct {
	dirs []string
	err  error
	at   time.Time
}

func resetBinPaths() {
	binPaths.Lock()
	binPaths.byKind = nil
	binPaths.Unlock()
}

// managerBinPaths is kind's global tool bin dirs, cached for
// capabilityTTL: `mise bin-paths`, `asdf current` + `asdf where`, or
// `nodenv prefix`, run from the neutral dir with the project-scoped
// settings scrubbed, keeping only dirs under the manager's installs.
func managerBinPaths(kind, shims string) ([]string, error) {
	binPaths.Lock()
	defer binPaths.Unlock()
	if c, ok := binPaths.byKind[kind]; ok && time.Since(c.at) < capabilityTTL {
		return c.dirs, c.err
	}
	dirs, err := listBinPaths(kind, shimIn(shims))
	if binPaths.byKind == nil {
		binPaths.byKind = map[string]cachedBinPaths{}
	}
	binPaths.byKind[kind] = cachedBinPaths{dirs, err, time.Now()}
	return dirs, err
}

func listBinPaths(kind, shim string) ([]string, error) {
	var raw []string
	switch kind {
	case managerMise:
		out, err := runManager(kind, shim, "bin-paths")
		if err != nil {
			return nil, err
		}
		raw = strings.Fields(out)
	case managerAsdf:
		out, err := runManager(kind, shim, "current")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || f[0] == "Name" {
				continue
			}
			where, err := runManager(kind, shim, "where", f[0])
			if err != nil {
				continue // listed but not installed
			}
			raw = append(raw, filepath.Join(strings.TrimSpace(where), "bin"))
		}
	case managerNodenv:
		out, err := runManager(kind, shim, "prefix")
		if err != nil {
			return nil, err
		}
		raw = []string{filepath.Join(strings.TrimSpace(out), "bin")}
	default:
		return nil, fmt.Errorf("%s has no global bin paths to list", kind)
	}
	var dirs []string
	for _, d := range raw {
		if !filepath.IsAbs(d) {
			continue
		}
		real, err := filepath.EvalSymlinks(d)
		if err != nil {
			continue
		}
		if st, err := os.Stat(real); err != nil || !st.IsDir() {
			continue
		}
		for _, installs := range installsDirs(kind) {
			if within(real, installs) {
				dirs = appendNew(dirs, d)
				break
			}
		}
	}
	return dirs, nil
}

// shimIn is one shim in the shims dir (managerTool finds mise through
// it), or "".
func shimIn(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return filepath.Join(dir, entries[0].Name())
}

// runManager runs `<manager> <args>` the way resolveShim does.
func runManager(kind, shim string, args ...string) (string, error) {
	tool, err := managerTool(kind, shim)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Dir = neutralDir()
	cmd.Env = scrubManagerEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", kind, strings.Join(args, " "), err)
	}
	return string(out), nil
}
