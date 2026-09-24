package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/autostart"
	"github.com/monoes/mono-agent/internal/monomind"
)

// After monomind.install, the monomind actually used is checked: an older
// one earlier on PATH is named (with the path to remove) instead of
// reporting success while the fix would be offered again next run.
func TestInstallMonomindChecksWhichCopyIsUsed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix names")
	}
	ctx := context.Background()
	binDir := t.TempDir()
	installed := filepath.Join(binDir, "monomind")
	os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755)
	install := func(_ context.Context, progress func(string)) (string, error) {
		progress("added 1 package")
		return binDir, nil
	}
	ok := func(context.Context, string) (*monomind.VersionInfo, error) {
		return &monomind.VersionInfo{Version: "2.20.0"}, nil
	}
	tooOld := func(context.Context, string) (*monomind.VersionInfo, error) {
		return nil, errors.New("monomind 1.0.0 is too old")
	}
	old := func() (string, error) { return "/usr/bin/monomind", nil }
	var lines []string
	progress := func(l string) { lines = append(lines, l) }

	err := installMonomind(ctx, install, old, tooOld, progress)
	if err == nil || !strings.Contains(err.Error(), "/usr/bin/monomind comes first on PATH") || !strings.Contains(err.Error(), installed) {
		t.Fatalf("shadowed by a broken copy: %v", err)
	}

	lines = nil
	if err := installMonomind(ctx, install, old, ok, progress); err != nil {
		t.Fatalf("shadowed by a working copy: %v", err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "is the one used") {
		t.Errorf("should note which copy is used: %q", lines)
	}

	lines = nil
	newOne := func() (string, error) { return installed, nil }
	if err := installMonomind(ctx, install, newOne, ok, progress); err != nil || !strings.Contains(strings.Join(lines, "\n"), "ready at "+installed) {
		t.Fatalf("the new copy is used: %v %q", err, lines)
	}
	if err := installMonomind(ctx, install, newOne, tooOld, progress); err == nil || !strings.Contains(err.Error(), "still does not answer") {
		t.Fatalf("new copy doesn't answer: %v", err)
	}

	clash := func(_ context.Context, progress func(string)) (string, error) {
		progress("npm error EEXIST: file already exists")
		progress("npm error File exists: /usr/local/bin/monomind")
		return "", errors.New("npm failed: exit status 1")
	}
	if err := installMonomind(ctx, clash, newOne, ok, progress); err == nil || !strings.Contains(err.Error(), "/usr/local/bin/monomind") {
		t.Fatalf("EEXIST not explained: %v", err)
	}
}

func TestDaemonArgsCarryDoctorsFlags(t *testing.T) {
	if got := daemonArgs(&globalConfig{DBPath: defaultDBPath}, "default"); strings.Join(got, " ") != "daemon" {
		t.Errorf("defaults: %v", got)
	}
	got := daemonArgs(&globalConfig{DBPath: "/tmp/x.db", ProfileID: "Work"}, "p-123")
	if strings.Join(got, " ") != "daemon --db-path /tmp/x.db --profile p-123" {
		t.Errorf("flags: %v", got)
	}
}

type fakeAutostart struct {
	installed bool
	started   int
}

func (f *fakeAutostart) Install(context.Context) (autostart.Result, error) {
	return autostart.Result{}, nil
}
func (f *fakeAutostart) Uninstall(context.Context) error { return nil }
func (f *fakeAutostart) Status(context.Context) (bool, string) {
	return f.installed, "/x/monoagent-daemon.service"
}
func (f *fakeAutostart) Start(context.Context) error { f.started++; return nil }

// The login service runs the daemon with the defaults: it is started only
// when doctor runs with them too.
func TestStartDaemonUsesTheServiceOnlyWithDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	as := &fakeAutostart{installed: true}
	if err := startDaemon(context.Background(), []string{"daemon"}, as, func(string) {}); err != nil || as.started != 1 {
		t.Fatalf("defaults: %v, started %d", err, as.started)
	}
	err := startDaemon(context.Background(), []string{"daemon", "--db-path", "/tmp/x.db"}, as, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "--db-path /tmp/x.db") || as.started != 1 {
		t.Fatalf("custom db: %v, started %d", err, as.started)
	}
}

func TestBridgeOwner(t *testing.T) {
	cases := []struct {
		pid, daemon   int
		command, unit string
		want          string
	}{
		{10, 10, "/usr/bin/monoagentcli daemon", "monoagent-daemon.service", "the daemon (pid 10), systemd service monoagent-daemon.service"},
		{11, 10, "monoagentcli extension serve", "monoagent-bridge.service", "`monoagentcli extension serve` (pid 11), systemd service monoagent-bridge.service"},
		{0, 10, "", "", ""},
		{12, 0, "", "", "pid 12"},
	}
	for _, c := range cases {
		if got := bridgeOwner(c.pid, c.daemon, c.command, c.unit); got != c.want {
			t.Errorf("bridgeOwner(%d,%d,%q,%q) = %q, want %q", c.pid, c.daemon, c.command, c.unit, got, c.want)
		}
	}
	if got := bridgeOwner(11, 10, "monoagentcli extension serve", ""); !strings.HasPrefix(got, "`monoagentcli extension serve` (pid 11)") {
		t.Errorf("ad-hoc serve: %q", got)
	}
}

func TestUnitFromCgroup(t *testing.T) {
	cases := map[string]string{
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/monoagent-bridge.service\n": "monoagent-bridge.service",
		"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-foot-1234.scope\n":      "",
		"0::/user.slice/user-1000.slice/user@1000.service\n":                                    "",
		"12:pids:/system.slice/x.service\n0::/\n":                                               "x.service",
	}
	for in, want := range cases {
		if got := unitFromCgroup(in); got != want {
			t.Errorf("unitFromCgroup(%q) = %q, want %q", in, got, want)
		}
	}
}
