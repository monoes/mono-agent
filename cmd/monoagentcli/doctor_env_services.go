package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/autostart"
	"github.com/monoes/mono-agent/internal/browserdetect"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/health"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/nodemgr"
)

// addServiceHooks wires the browser, services and integrations checks to
// the real machine. cfg carries doctor's --db-path/--profile to a daemon
// it starts.
func addServiceHooks(env *health.Env, cfg *globalConfig) {
	env.FindBrowser = browserdetect.FindBrowser
	env.ExtensionInstalled = browserdetect.ExtensionInstalled
	env.ExtensionDir = browserdetect.ExtensionDir
	env.Bridge = func(context.Context) (health.BridgeInfo, bool) {
		st, addr, ok := findRunningBridge() // read-only GET of /monoagent/health
		if !ok {
			return health.BridgeInfo{}, false
		}
		hb, live := daemonhb.Read()
		daemonPID := 0
		if live {
			daemonPID = hb.PID
		}
		return health.BridgeInfo{Addr: bridgeAddr(st, addr), Status: st.Status, PID: st.PID, Version: st.Version, UptimeSec: st.UptimeSec,
			Owner: bridgeOwner(st.PID, daemonPID, processCommand(st.PID), serviceUnit(st.PID))}, true
	}

	env.Daemon = func(context.Context) health.DaemonInfo {
		hb, live := daemonhb.Read()
		return health.DaemonInfo{Running: live, PID: hb.PID, APIAddr: hb.APIAddr, BridgeAddr: hb.BridgeAddr, AgeMS: time.Since(hb.TS).Milliseconds()}
	}
	env.APIHealth = func(ctx context.Context, addr string) error {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET /health: HTTP %d", resp.StatusCode)
		}
		return nil
	}
	env.AutostartStatus = autostart.New().Status
	env.InstallAutostart = func(ctx context.Context, progress func(string)) error {
		res, err := autostart.New().Install(ctx)
		if err != nil {
			return err
		}
		progress(res.Description)
		return nil
	}
	env.StartDaemon = func(ctx context.Context, progress func(string)) error {
		// The profile as doctor resolved it: --profile may be a name.
		return startDaemon(ctx, daemonArgs(cfg, env.ProfileID), autostart.New(), progress)
	}
	env.StopDaemon = stopDaemon

	env.ClaudeSkills = claudeSkillsState
	env.InstallClaudeSkills = func() error { return installClaudeSkill(false) }
	env.MCPRegistration = claudeMCPRegistration
	env.RegisterMCP = registerClaudeMCP
}

// startDaemon starts `monoagentcli daemon` through its login service when
// one is registered (idempotent), otherwise as a detached background
// process logging to ~/.monoagent/logs/daemon.log. The daemon gets
// doctor's --db-path and --profile; the login service runs with the
// defaults, so it is not used when those were changed.
func startDaemon(ctx context.Context, args []string, as autostart.Installer, progress func(string)) error {
	// A daemon that is still starting has no heartbeat yet, but it holds
	// the lock: starting another would be refused by it anyway, and must
	// not be spawned while one comes up (a second click, a re-check).
	if daemonhb.Locked() {
		progress("a daemon is already running or starting")
		return nil
	}
	if ok, where := as.Status(ctx); ok {
		if len(args) > 1 {
			// One daemon per home: the service's daemon would take the
			// lock, on the default database and profile.
			return fmt.Errorf("the registered login service (%s) runs the daemon with the default database and profile, not %s — "+
				"start it yourself: monoagentcli %s", where, strings.Join(args[1:], " "), strings.Join(args, " "))
		}
		progress("starting the registered service (" + where + ")")
		return as.Start(ctx)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".monoagent", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, "daemon.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd, err = monomind.StartDetached(cmd)
	if err != nil {
		return fmt.Errorf("starting the daemon: %w", err)
	}
	progress(fmt.Sprintf("started `%s %s` in the background (pid %d, log %s)", filepath.Base(exe), strings.Join(args, " "), cmd.Process.Pid, logPath))
	return cmd.Process.Release()
}

// daemonStopPollInterval is how often stopDaemon re-checks the daemon's
// single-instance lock while waiting for it to clear.
const daemonStopPollInterval = 300 * time.Millisecond

// daemonStopGrace is how long stopDaemon waits for a SIGTERM'd daemon to
// finish its own graceful shutdown (draining in-flight workflow executions,
// see daemon.go) before it escalates to a forced kill.
const daemonStopGrace = 15 * time.Second

// stopDaemon stops the daemon at pid so a caller can start a fresh one on
// the current binary without racing its own shutdown: SIGTERM, then poll
// daemonhb.Locked() — the OS releases that lock on exit even after a crash,
// so it is the one race-free signal that nothing still holds the bridge
// port or the schedule lock — escalating to SIGKILL if it hasn't cleared
// within daemonStopGrace.
func stopDaemon(ctx context.Context, pid int, progress func(string)) error {
	if !daemonhb.Locked() {
		return nil // already stopped
	}
	if err := terminateProcess(pid); err != nil && daemonhb.Locked() {
		return fmt.Errorf("signaling pid %d: %w", pid, err)
	}
	deadline := time.Now().Add(daemonStopGrace)
	forced := false
	for daemonhb.Locked() {
		if time.Now().After(deadline) {
			if forced {
				return fmt.Errorf("pid %d would not stop even after SIGKILL", pid)
			}
			progress(fmt.Sprintf("pid %d did not stop within %s — forcing it", pid, daemonStopGrace))
			if err := killProcess(pid); err != nil {
				return fmt.Errorf("force-stopping pid %d: %w", pid, err)
			}
			forced = true
			deadline = time.Now().Add(5 * time.Second)
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(daemonStopPollInterval):
		}
	}
	progress(fmt.Sprintf("stopped pid %d", pid))
	return nil
}

// daemonArgs is `daemon` plus the --db-path and --profile doctor runs
// with, when they were given: profileID is the profile as doctor resolved
// it (--profile may be a name).
func daemonArgs(cfg *globalConfig, profileID string) []string {
	args := []string{"daemon"}
	if cfg == nil {
		return args
	}
	if cfg.DBPath != "" && expandPath(cfg.DBPath) != expandPath(defaultDBPath) {
		args = append(args, "--db-path", expandPath(cfg.DBPath))
	}
	if cfg.ProfileID != "" && profileID != "" {
		args = append(args, "--profile", profileID)
	}
	return args
}

// bridgeOwner says what runs the bridge process pid: the daemon (pid
// daemonPID), an `extension serve`, under a systemd/launchd service or by
// hand. command is its command line and unit its systemd unit ("" when
// unknown or none).
func bridgeOwner(pid, daemonPID int, command, unit string) string {
	if pid <= 0 {
		return ""
	}
	var what string
	switch {
	case pid == daemonPID:
		what = fmt.Sprintf("the daemon (pid %d)", pid)
	case strings.Contains(command, "extension serve"):
		what = fmt.Sprintf("`monoagentcli extension serve` (pid %d)", pid)
	case strings.Contains(command, " daemon"):
		what = fmt.Sprintf("a daemon that is not this home's (pid %d: %s)", pid, command)
	case command != "":
		what = fmt.Sprintf("pid %d: %s", pid, command)
	default:
		return fmt.Sprintf("pid %d", pid)
	}
	switch {
	case unit != "":
		what += ", systemd service " + unit
	case pid != daemonPID && command != "" && runtime.GOOS == "linux":
		what += ", started by hand"
	}
	return what
}

// processCommand is pid's command line, "" when it can't be read cheaply
// (Linux: /proc; macOS: ps; Windows: not looked up).
func processCommand(pid int) string {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return ""
}

// serviceUnit is the systemd service pid runs under (Linux), from its
// cgroup: the last path element when it is a "*.service" other than the
// user manager itself. "" otherwise (a terminal's scope, not Linux).
func serviceUnit(pid int) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return ""
	}
	return unitFromCgroup(string(b))
}

func unitFromCgroup(cgroup string) string {
	for _, line := range strings.Split(strings.TrimSpace(cgroup), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		last := filepath.Base(parts[2])
		if strings.HasSuffix(last, ".service") && !strings.HasPrefix(last, "user@") {
			return last
		}
	}
	return ""
}

// claudeMCPRegistration looks for a user-scope MCP server in
// ~/.claude.json that runs `monoagentcli mcp`.
func claudeMCPRegistration() (claudeFound, registered bool, where string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, false, ""
	}
	path := filepath.Join(home, ".claude.json")
	b, err := os.ReadFile(path)
	if err != nil {
		_, derr := os.Stat(filepath.Join(home, ".claude"))
		return derr == nil, false, ""
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return true, false, ""
	}
	for name, s := range cfg.MCPServers {
		base := strings.TrimSuffix(filepath.Base(s.Command), ".exe")
		if base == "monoagentcli" && len(s.Args) > 0 && s.Args[0] == "mcp" {
			return true, true, fmt.Sprintf("%q in %s", name, path)
		}
	}
	return true, false, ""
}

// registerClaudeMCP runs `claude mcp add --scope user monoagent -- <this
// binary> mcp`.
func registerClaudeMCP(ctx context.Context, progress func(string)) error {
	claude, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("the claude CLI is not on PATH — install Claude Code first (monoagentcli agent install claude)")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, claude, "mcp", "add", "--scope", "user", "monoagent", "--", exe, "mcp")
	return nodemgr.StreamCmd(cmd, progress)
}
