package monomind

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Version managers whose shims pick the real binary per directory (#301).
// A shim run in a project root lets that project's .tool-versions or
// mise.toml choose what runs, so a shim is resolved once from a neutral
// directory and never executed itself.
const (
	managerMise  = "mise"
	managerAsdf  = "asdf"
	managerVolta = "volta"
)

// pinned is a binary Find resolved through any shim, with the directory of
// the node that runs it ("" when it isn't a node script or no node exists).
type pinned struct {
	bin     string
	nodeDir string
	at      time.Time
}

var pins struct {
	sync.Mutex
	byFound map[string]pinned // Find's candidate path → its pin
	nodeDir map[string]string // pinned bin → nodeDir
}

// resetPins drops every cached pin (ResetCapabilityCache).
func resetPins() {
	pins.Lock()
	pins.byFound, pins.nodeDir = nil, nil
	pins.Unlock()
}

// pin resolves found (an absolute candidate path) to the binary that runs
// no matter the working directory, cached for capabilityTTL.
func pin(found string) (pinned, error) {
	pins.Lock()
	defer pins.Unlock()
	if p, ok := pins.byFound[found]; ok && time.Since(p.at) < capabilityTTL {
		if _, err := os.Stat(p.bin); err == nil {
			return p, nil
		}
	}
	bin, err := pinPath(found, "monomind")
	if err != nil {
		return pinned{}, err
	}
	nodeDir, err := pinNodeDir(bin)
	if err != nil {
		return pinned{}, err
	}
	p := pinned{bin: bin, nodeDir: nodeDir, at: time.Now()}
	if pins.byFound == nil {
		pins.byFound, pins.nodeDir = map[string]pinned{}, map[string]string{}
	}
	pins.byFound[found] = p
	pins.nodeDir[bin] = nodeDir
	return p, nil
}

// pinPath returns path itself, or for a version-manager shim the binary
// named name that the manager picks from a neutral directory, validated to
// sit under that manager's installs dir.
func pinPath(path, name string) (string, error) {
	kind := shimManager(path)
	if kind == "" {
		return path, nil
	}
	return resolveShim(kind, path, name)
}

// shimManager names the version manager whose shim path is, or "". It goes
// by what the path resolves to (the mise/rtx binary, volta-shim), an asdf
// or mise shim script's header, a mise/asdf shims dir anywhere in the path,
// and the managers' configured shims dirs.
func shimManager(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(real)), ".exe") {
	case "mise", "rtx":
		return managerMise
	case "volta-shim":
		return managerVolta
	}
	if head := fileHead(real, 1024); strings.HasPrefix(head, "#!") {
		switch {
		case strings.Contains(head, "asdf exec"), strings.Contains(head, "# asdf-plugin"):
			return managerAsdf
		case strings.Contains(head, "mise x "), strings.Contains(head, "mise exec"), strings.Contains(head, "rtx exec"):
			return managerMise
		}
	}
	switch p := filepath.ToSlash(path); {
	case strings.Contains(p, "/mise/shims/"), strings.Contains(p, "/rtx/shims/"):
		return managerMise
	case strings.Contains(p, "/asdf/shims/"), strings.Contains(p, "/.asdf/shims/"):
		return managerAsdf
	}
	for _, dir := range []string{filepath.Dir(path), filepath.Dir(real)} {
		for _, kind := range []string{managerMise, managerAsdf, managerVolta} {
			for _, shims := range shimDirs(kind) {
				if sameDir(dir, shims) {
					return kind
				}
			}
		}
	}
	return ""
}

// managerDataDirs are the roots a manager keeps its shims and installs in:
// the env override first, then the defaults.
func managerDataDirs(kind string) []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	add := func(d string) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	join := func(elem ...string) string {
		if home == "" {
			return ""
		}
		return filepath.Join(append([]string{home}, elem...)...)
	}
	switch kind {
	case managerMise:
		add(os.Getenv("MISE_DATA_DIR"))
		add(os.Getenv("RTX_DATA_DIR"))
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			add(filepath.Join(xdg, "mise"))
			add(filepath.Join(xdg, "rtx"))
		}
		add(join(".local", "share", "mise"))
		add(join(".local", "share", "rtx"))
		if runtime.GOOS == "windows" {
			if la := os.Getenv("LOCALAPPDATA"); la != "" {
				add(filepath.Join(la, "mise"))
			}
		}
	case managerAsdf:
		add(os.Getenv("ASDF_DATA_DIR"))
		add(join(".asdf"))
	case managerVolta:
		add(os.Getenv("VOLTA_HOME"))
		add(join(".volta"))
		if runtime.GOOS == "windows" {
			if la := os.Getenv("LOCALAPPDATA"); la != "" {
				add(filepath.Join(la, "Volta"))
			}
		}
	}
	return dirs
}

func shimDirs(kind string) []string {
	sub := "shims"
	if kind == managerVolta {
		sub = "bin"
	}
	var out []string
	for _, d := range managerDataDirs(kind) {
		out = append(out, filepath.Join(d, sub))
	}
	return out
}

func installsDirs(kind string) []string {
	var out []string
	for _, d := range managerDataDirs(kind) {
		if kind == managerVolta {
			out = append(out, filepath.Join(d, "tools", "image"))
		} else {
			out = append(out, filepath.Join(d, "installs"))
		}
	}
	return out
}

// managerTool finds the manager's own executable: the binary a mise shim
// links to, else PATH, else the manager's default location.
func managerTool(kind, shim string) (string, error) {
	if kind == managerMise {
		if real, err := filepath.EvalSymlinks(shim); err == nil {
			if b := strings.TrimSuffix(strings.ToLower(filepath.Base(real)), ".exe"); b == "mise" || b == "rtx" {
				return real, nil
			}
		}
	}
	for _, name := range managerNames(kind) {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	for _, d := range managerDataDirs(kind) {
		p := filepath.Join(d, "bin", kind)
		if kind == managerVolta {
			p = filepath.Join(d, "bin", "volta")
		}
		if isExecutableFile(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("monomind is a %s shim, and %s is not on PATH to resolve it", kind, kind)
}

func managerNames(kind string) []string {
	if kind == managerMise {
		return []string{"mise", "rtx"}
	}
	return []string{kind}
}

// resolveShim is `<manager> which <name>` run from a neutral directory
// with the project-scoped manager settings removed from its environment,
// so no project's version files take part. The result must resolve to a
// file under the manager's installs dir.
func resolveShim(kind, shim, name string) (string, error) {
	tool, err := managerTool(kind, shim)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, "which", name)
	cmd.Dir = neutralDir()
	cmd.Env = scrubManagerEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s is a %s shim, and `%s which %s` failed: %w", shim, kind, kind, name, err)
	}
	path := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("`%s which %s` gave %q, not an absolute path", kind, name, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("`%s which %s` gave %s: %w", kind, name, path, err)
	}
	if !isExecutableFile(real) {
		return "", fmt.Errorf("`%s which %s` gave %s, which is not an executable file", kind, name, path)
	}
	for _, dir := range installsDirs(kind) {
		if within(real, dir) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s resolved through its %s shim to %s, outside %s's installs dir; refusing", name, kind, real, kind)
}

// neutralDir is where shims are resolved: the user's home, which no
// project controls, else the filesystem root.
func neutralDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		if st, err := os.Stat(home); err == nil && st.IsDir() {
			return home
		}
	}
	return string(filepath.Separator)
}

// managerLocationVars are the manager settings kept when resolving a shim:
// where its data and user-level config live. Every other MISE_*, RTX_*,
// ASDF_* or VOLTA_* variable (a pinned tool version, an extra config file
// name, an activated shell's per-directory state) is dropped.
var managerLocationVars = map[string]bool{
	"MISE_DATA_DIR": true, "MISE_CONFIG_DIR": true, "MISE_CACHE_DIR": true, "MISE_STATE_DIR": true,
	"MISE_GLOBAL_CONFIG_FILE": true, "MISE_CEILING_PATHS": true,
	"RTX_DATA_DIR": true, "RTX_CONFIG_DIR": true, "RTX_CACHE_DIR": true,
	"ASDF_DATA_DIR": true, "ASDF_DIR": true, "ASDF_CONFIG_FILE": true,
	"VOLTA_HOME": true,
}

func scrubManagerEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		k := strings.ToUpper(strings.TrimLeft(key, "_"))
		managed := false
		for _, prefix := range []string{"MISE_", "RTX_", "ASDF_", "VOLTA_"} {
			if strings.HasPrefix(k, prefix) {
				managed = true
				break
			}
		}
		if managed && !(key == k && managerLocationVars[k]) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// pinNodeDir finds the node that runs bin when bin is a node script: the
// one next to it, else the one on PATH, resolved through a shim the same
// way. "" when bin doesn't run on node or no node is installed (the exec
// then fails as it always did).
func pinNodeDir(bin string) (string, error) {
	if !runsOnNode(bin) {
		return "", nil
	}
	node := filepath.Join(filepath.Dir(bin), "node")
	if runtime.GOOS == "windows" {
		node += ".exe"
	}
	if !isExecutableFile(node) {
		p, err := exec.LookPath("node")
		if err != nil {
			return "", nil
		}
		if node, err = filepath.Abs(p); err != nil {
			node = p
		}
	}
	node, err := pinPath(node, "node")
	if err != nil {
		return "", err
	}
	return filepath.Dir(node), nil
}

// runsOnNode reports whether bin is a script whose interpreter line names
// node (`#!/usr/bin/env node`), or an npm .cmd wrapper on Windows.
func runsOnNode(bin string) bool {
	if strings.EqualFold(filepath.Ext(bin), ".cmd") {
		return true
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		real = bin
	}
	head := fileHead(real, 256)
	if !strings.HasPrefix(head, "#!") {
		return false
	}
	line, _, _ := strings.Cut(head, "\n")
	return strings.Contains(line, "node")
}

// PinEnv returns env with PATH led by bin's pinned node dir and bin's own
// dir, so `#!/usr/bin/env node` (and any node monomind starts by name)
// resolves to the pinned interpreter whatever the working directory.
func PinEnv(env []string, bin string) []string {
	dirs := []string{}
	if nd := nodeDirFor(bin); nd != "" {
		dirs = append(dirs, nd)
	}
	if filepath.IsAbs(bin) && (len(dirs) == 0 || dirs[0] != filepath.Dir(bin)) {
		dirs = append(dirs, filepath.Dir(bin))
	}
	if len(dirs) == 0 {
		return env
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
	parts := append([]string(nil), dirs...)
	for _, p := range filepath.SplitList(cur) {
		dup := false
		for _, d := range dirs {
			if p == d {
				dup = true
			}
		}
		if p != "" && !dup {
			parts = append(parts, p)
		}
	}
	return append(out, key+"="+strings.Join(parts, string(os.PathListSeparator)))
}

// nodeDirFor is bin's pinned node dir, pinned now when bin didn't come
// from Find (a caller-supplied path).
func nodeDirFor(bin string) string {
	pins.Lock()
	nd, ok := pins.nodeDir[bin]
	pins.Unlock()
	if ok {
		return nd
	}
	nd, _ = pinNodeDir(bin)
	return nd
}

// Command is exec.Command for a monomind binary: its environment is this
// process's through PinEnv. Callers that set cmd.Env themselves pass it
// through PinEnv.
func Command(bin string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	cmd.Env = PinEnv(os.Environ(), bin)
	return cmd
}

// CommandContext is Command with exec.CommandContext.
func CommandContext(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = PinEnv(os.Environ(), bin)
	return cmd
}

// CheckOutside fails when bin, or the node pinned to run it, lies inside
// any of roots (by path or by what it resolves to): a project-local binary
// is the project's to swap, so it never runs for that project.
func CheckOutside(bin string, roots ...string) error {
	paths := []string{bin}
	if nd := nodeDirFor(bin); nd != "" {
		paths = append(paths, nd)
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			rootAbs = root
		}
		rootReal, err := filepath.EvalSymlinks(rootAbs)
		if err != nil {
			rootReal = rootAbs
		}
		for _, p := range paths {
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			real, err := filepath.EvalSymlinks(abs)
			if err != nil {
				real = abs
			}
			for _, r := range []string{rootAbs, rootReal} {
				if within(abs, r) || within(real, r) {
					return fmt.Errorf("monomind resolved to a project-local binary (%s is inside %s); refusing to run it", p, root)
				}
			}
		}
	}
	return nil
}

// EnsureIn is Ensure for a command that runs in projectRoot: the project
// check comes before the handshake, which already runs the binary.
func EnsureIn(ctx context.Context, projectRoot string) (string, error) {
	bin, err := findIn(projectRoot)
	if err != nil {
		return "", err
	}
	if _, err := Handshake(ctx, bin); err != nil {
		return "", err
	}
	return bin, nil
}

// findIn is Find for a command that runs in projectRoot.
func findIn(projectRoot string) (string, error) {
	bin, err := Find()
	if err != nil {
		return "", err
	}
	if err := CheckOutside(bin, projectRoot); err != nil {
		return "", err
	}
	return bin, nil
}

// within reports whether path is dir or inside it (dir resolved through
// symlinks too, so a symlinked data dir still matches).
func within(path, dir string) bool {
	check := func(d string) bool {
		rel, err := filepath.Rel(d, path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	if check(filepath.Clean(dir)) {
		return true
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return check(real)
	}
	return false
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

func fileHead(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, n)
	k, _ := io.ReadFull(f, buf)
	return string(buf[:k])
}

func isExecutableFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || st.Mode()&0o111 != 0
}
