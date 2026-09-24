package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
// the real machine.
func addServiceHooks(env *health.Env) {
	env.FindBrowser = browserdetect.FindBrowser
	env.ExtensionInstalled = browserdetect.ExtensionInstalled
	env.ExtensionDir = browserdetect.ExtensionDir
	env.Bridge = func(context.Context) (health.BridgeInfo, bool) {
		st, addr, ok := findRunningBridge() // read-only GET of /monoagent/health
		if !ok {
			return health.BridgeInfo{}, false
		}
		return health.BridgeInfo{Addr: bridgeAddr(st, addr), Status: st.Status, PID: st.PID, Version: st.Version, UptimeSec: st.UptimeSec}, true
	}

	env.Daemon = func(context.Context) health.DaemonInfo {
		hb, live := daemonhb.Read()
		return health.DaemonInfo{Running: live, PID: hb.PID, APIAddr: hb.APIAddr, AgeMS: time.Since(hb.TS).Milliseconds()}
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
	env.StartDaemon = startDaemon

	env.ClaudeSkills = claudeSkillsState
	env.InstallClaudeSkills = func() error { return installClaudeSkill(false) }
	env.MCPRegistration = claudeMCPRegistration
	env.RegisterMCP = registerClaudeMCP
}

// startDaemon starts `monoagentcli daemon` through its login service when
// one is registered (idempotent), otherwise as a detached background
// process logging to ~/.monoagent/logs/daemon.log.
func startDaemon(ctx context.Context, progress func(string)) error {
	// A daemon that is still starting has no heartbeat yet, but it holds
	// the lock: starting another would be refused by it anyway, and must
	// not be spawned while one comes up (a second click, a re-check).
	if daemonhb.Locked() {
		progress("a daemon is already running or starting")
		return nil
	}
	as := autostart.New()
	if ok, where := as.Status(ctx); ok {
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
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd, err = monomind.StartDetached(cmd)
	if err != nil {
		return fmt.Errorf("starting the daemon: %w", err)
	}
	progress(fmt.Sprintf("started `%s daemon` in the background (pid %d, log %s)", filepath.Base(exe), cmd.Process.Pid, logPath))
	return cmd.Process.Release()
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
