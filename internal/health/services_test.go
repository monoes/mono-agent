package health

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserChecks(t *testing.T) {
	ctx := context.Background()
	env := &Env{FindBrowser: func() string { return "" }, ExtensionInstalled: func() bool { return false },
		ExtensionDir: func() string { return filepath.Join(string(filepath.Separator), "x", "chrome-extension") }}
	if res := checkBrowser(ctx, env); res.Status != StatusWarn || res.FixID != FixBrowserInstall {
		t.Errorf("no browser: %+v", res)
	}
	if res := checkExtension(ctx, env); res.Status != StatusWarn || res.Detail != "load unpacked from: "+filepath.Join(string(filepath.Separator), "x", "chrome-extension") {
		t.Errorf("no extension: %+v", res)
	}

	env.Bridge = func(context.Context) (BridgeInfo, bool) { return BridgeInfo{}, false }
	if res := checkBridge(ctx, env); res.Status != StatusWarn || res.FixID != FixDaemonStart {
		t.Errorf("no bridge: %+v", res)
	}
	for status, want := range map[string]Status{"connected": StatusOK, "unpaired": StatusWarn, "waiting": StatusInfo} {
		b := BridgeInfo{Addr: "127.0.0.1:9222", Status: status, Version: "v1.2.0"}
		env.Bridge = func(context.Context) (BridgeInfo, bool) { return b, true }
		if res := checkPaired(ctx, env); res.Status != want {
			t.Errorf("bridge %s: %q, want %q", status, res.Status, want)
		}
	}
	env.Version = "v1.3.0"
	if res := checkBridge(ctx, env); res.Status != StatusWarn {
		t.Errorf("version skew: %+v", res)
	}
	env.Version = "v1.2.0-3-gabc"
	if res := checkBridge(ctx, env); res.Status != StatusOK {
		t.Errorf("dev build must not report skew: %+v", res)
	}
}

func TestDaemonCheckAndStart(t *testing.T) {
	ctx := context.Background()
	running := false
	started := 0
	env := &Env{
		Daemon:      func(context.Context) DaemonInfo { return DaemonInfo{Running: running, PID: 7, APIAddr: "127.0.0.1:1"} },
		APIHealth:   func(context.Context, string) error { return nil },
		StartDaemon: func(context.Context, func(string)) error { started++; running = true; return nil },
	}
	if res := checkDaemon(ctx, env); res.Status != StatusWarn || res.FixID != FixDaemonStart {
		t.Fatalf("stopped: %+v", res)
	}
	if err := fixDaemonStart(ctx, env, noop); err != nil || started != 1 {
		t.Fatalf("start: %v (started %d)", err, started)
	}
	// Idempotent: a running daemon is not started twice.
	if err := fixDaemonStart(ctx, env, noop); err != nil || started != 1 {
		t.Fatalf("second start: %v (started %d)", err, started)
	}
	if res := checkDaemon(ctx, env); res.Status != StatusOK {
		t.Fatalf("running: %+v", res)
	}
	env.APIHealth = func(context.Context, string) error { return errors.New("refused") }
	if res := checkDaemon(ctx, env); res.Status != StatusWarn {
		t.Fatalf("API down: %+v", res)
	}
}

func TestAutostartIsOptionalInfo(t *testing.T) {
	env := &Env{AutostartStatus: func(context.Context) (bool, string) { return false, "" }}
	res := checkAutostart(context.Background(), env)
	if res.Status != StatusInfo || res.FixID != FixAutostart {
		t.Fatalf("%+v", res)
	}
	f, _ := Default().Fix(FixAutostart)
	if !f.Optional {
		t.Error("registering a login item must be optional")
	}
}

func TestIntegrationChecks(t *testing.T) {
	ctx := context.Background()
	env := &Env{ClaudeSkills: func() (bool, []string, []string) { return false, nil, nil }}
	if res := checkClaudeSkills(ctx, env); res.Status != StatusSkip {
		t.Errorf("no claude: %+v", res)
	}
	env.ClaudeSkills = func() (bool, []string, []string) { return true, nil, []string{"a.md"} }
	if res := checkClaudeSkills(ctx, env); res.Status != StatusWarn || res.FixID != FixClaudeSkills {
		t.Errorf("stale: %+v", res)
	}
	env.MCPRegistration = func() (bool, bool, string) { return true, false, "" }
	if res := checkMCP(ctx, env); res.Status != StatusInfo || res.FixID != FixMCPRegister {
		t.Errorf("unregistered: %+v", res)
	}
	env.MCPRegistration = func() (bool, bool, string) { return true, true, "x" }
	if res := checkMCP(ctx, env); res.Status != StatusOK {
		t.Errorf("registered: %+v", res)
	}
}

// With a daemon running, "start the daemon" can't bring the bridge up:
// the check says why instead of offering it.
func TestBridgeDownWithTheDaemonRunning(t *testing.T) {
	ctx := context.Background()
	d := DaemonInfo{Running: true, PID: 42}
	env := &Env{Bridge: func(context.Context) (BridgeInfo, bool) { return BridgeInfo{}, false },
		Daemon: func(context.Context) DaemonInfo { return d }}
	res := checkBridge(ctx, env)
	if res.FixID != "" || !strings.Contains(res.Summary, "pid 42") || !strings.Contains(res.Detail, "--bridge=false") {
		t.Errorf("daemon without bridge: %+v", res)
	}
	d.BridgeAddr = "127.0.0.1:9222"
	if res := checkBridge(ctx, env); res.FixID != "" || !strings.Contains(res.Summary, "127.0.0.1:9222") {
		t.Errorf("daemon bridge not answering: %+v", res)
	}
	d.Running = false
	if res := checkBridge(ctx, env); res.FixID != FixDaemonStart {
		t.Errorf("no daemon: %+v", res)
	}
}

func TestBridgeSaysWhoRunsIt(t *testing.T) {
	env := &Env{Bridge: func(context.Context) (BridgeInfo, bool) {
		return BridgeInfo{Addr: "127.0.0.1:9222", PID: 7, Owner: "`monoagentcli extension serve` (pid 7), systemd service mybridge.service"}, true
	}}
	if res := checkBridge(context.Background(), env); !strings.Contains(res.Summary, "run by `monoagentcli extension serve`") {
		t.Errorf("owner missing: %+v", res)
	}
}

// A unit file systemd doesn't have enabled is not "starts at login", and
// the row says why.
func TestAutostartExplainsANotEnabledEntry(t *testing.T) {
	env := &Env{AutostartStatus: func(context.Context) (bool, string) { return false, "unit exists but systemd reports it disabled" }}
	res := checkAutostart(context.Background(), env)
	if res.Status != StatusInfo || res.FixID != FixAutostart || !strings.Contains(res.Detail, "disabled") {
		t.Errorf("%+v", res)
	}
}
