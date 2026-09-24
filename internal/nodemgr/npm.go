package nodemgr

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// NpmPlan says how a global npm install will run.
type NpmPlan struct {
	Npm     string   // npm executable
	Env     []string // full environment for it
	Prefix  string   // global prefix packages land in
	BinDir  string   // where their executables appear
	Managed bool     // true when the managed Node runs it
}

// PlanGlobalInstall picks the npm for `npm install -g`:
//   - a suitable system Node's npm, into its own global prefix when that is
//     writable, otherwise into ~/.monoagent/npm-global (never sudo);
//   - else the managed Node's npm, into ~/.monoagent/npm-global.
func (m *Manager) PlanGlobalInstall(ctx context.Context) (*NpmPlan, error) {
	if nodePath, v, ok := m.SystemNode(ctx); ok && Suitable(v) {
		npm := siblingNpm(nodePath, m.GOOS)
		if npm == "" {
			if p, err := exec.LookPath("npm"); err == nil {
				npm = p
			}
		}
		if npm != "" {
			plan := &NpmPlan{Npm: npm, Env: os.Environ()}
			if prefix := npmGlobalPrefix(ctx, npm); prefix != "" && writable(prefix, m.GOOS) {
				plan.Prefix = prefix
				plan.BinDir = prefixBin(prefix, m.GOOS)
				return plan, nil
			}
			plan.Prefix, plan.BinDir = m.NpmRoot, m.NpmBinDir()
			plan.Env = withEnv(plan.Env, "NPM_CONFIG_PREFIX", m.NpmRoot)
			return plan, nil
		}
	}
	if v, ok := m.Current(); ok {
		return &NpmPlan{Npm: m.NpmPath(v), Env: m.NpmEnv(v), Prefix: m.NpmRoot, BinDir: m.NpmBinDir(), Managed: true}, nil
	}
	return nil, fmt.Errorf("no Node.js >= %s — install one first (monoagentcli nodejs install)", MinVersion)
}

// InstallGlobal runs `npm install -g <pkgs>` per PlanGlobalInstall,
// streaming output, and makes sure the resulting executables are on PATH.
func (m *Manager) InstallGlobal(ctx context.Context, progress func(string), pkgs ...string) (*NpmPlan, error) {
	plan, err := m.PlanGlobalInstall(ctx)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(plan.Prefix, 0o755); err != nil {
		return nil, err
	}
	args := append([]string{"install", "-g"}, pkgs...)
	progress(fmt.Sprintf("running: %s %s (prefix %s)", plan.Npm, strings.Join(args, " "), plan.Prefix))
	cmd := exec.CommandContext(ctx, plan.Npm, args...)
	cmd.Env = plan.Env
	if err := StreamCmd(cmd, progress); err != nil {
		return nil, err
	}
	appendPath(plan.BinDir)
	return plan, nil
}

func siblingNpm(nodePath, goos string) string {
	name := "npm"
	if goos == "windows" {
		name = "npm.cmd"
	}
	p := filepath.Join(filepath.Dir(nodePath), name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func npmGlobalPrefix(ctx context.Context, npm string) string {
	out, err := exec.CommandContext(ctx, npm, "prefix", "-g").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func prefixBin(prefix, goos string) string {
	if goos == "windows" {
		return prefix
	}
	return filepath.Join(prefix, "bin")
}

// writable reports whether npm could install into prefix without root:
// both its lib/node_modules (created if missing) and bin must be writable.
func writable(prefix, goos string) bool {
	dirs := []string{prefixBin(prefix, goos), filepath.Join(prefix, "lib", "node_modules")}
	if goos == "windows" {
		dirs = []string{prefix}
	}
	for _, d := range dirs {
		probe := d
		for {
			if _, err := os.Stat(probe); err == nil {
				break
			}
			parent := filepath.Dir(probe)
			if parent == probe {
				return false
			}
			probe = parent
		}
		f, err := os.CreateTemp(probe, ".monoagent-probe-*")
		if err != nil {
			return false
		}
		f.Close()
		os.Remove(f.Name())
	}
	return true
}

func withEnv(env []string, key, val string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, key) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+val)
}

// StreamCmd runs cmd with stdout+stderr passed line by line to progress;
// a failure carries the last output lines.
func StreamCmd(cmd *exec.Cmd, progress func(string)) error {
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var mu sync.Mutex
	var tail []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			progress(line)
			mu.Lock()
			if tail = append(tail, line); len(tail) > 5 {
				tail = tail[1:]
			}
			mu.Unlock()
		}
		io.Copy(io.Discard, pr)
	}()
	err := cmd.Run()
	pw.Close()
	<-done
	if err != nil {
		mu.Lock()
		defer mu.Unlock()
		return fmt.Errorf("%s failed: %v\n%s", filepath.Base(cmd.Path), err, strings.Join(tail, "\n"))
	}
	return nil
}
