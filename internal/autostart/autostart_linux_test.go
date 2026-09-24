//go:build linux

package autostart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The binary path is quoted in ExecStart, so a folder with a space, a % or
// a $ in its name still names one file to systemd.
func TestRenderUnitQuotesTheBinaryPath(t *testing.T) {
	unit, err := renderUnit(`/home/a b/100%/$x/monoagentcli`)
	if err != nil {
		t.Fatal(err)
	}
	want := `ExecStart="/home/a b/100%%/$$x/monoagentcli" daemon`
	if !strings.Contains(unit, want+"\n") {
		t.Fatalf("unit has no %q:\n%s", want, unit)
	}
	if !strings.Contains(unit, "WantedBy=default.target") {
		t.Fatalf("unit is not enabled for the user session:\n%s", unit)
	}
}

func TestSystemdQuoteEscapesQuotesAndBackslashes(t *testing.T) {
	if got, want := systemdQuote(`/a"b\c`), `"/a\"b\\c"`; got != want {
		t.Fatalf("systemdQuote = %s, want %s", got, want)
	}
}

// A unit file alone isn't "starts at login": systemd must have it enabled.
func TestStatusNeedsTheUnitEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctx := context.Background()
	var state string
	var calls [][]string
	origCtl, origAvail := systemctl, systemdAvailable
	t.Cleanup(func() { systemctl, systemdAvailable = origCtl, origAvail })
	systemdAvailable = func() bool { return true }
	systemctl = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if state == "enabled" {
			return []byte("enabled\n"), nil
		}
		return []byte(state + "\n"), errors.New("exit status 1")
	}
	in := linuxInstaller{}
	if ok, where := in.Status(ctx); ok || where != "" || len(calls) != 0 {
		t.Fatalf("no unit file: %v %q, systemctl calls %v", ok, where, calls)
	}
	path, _ := unitPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("[Unit]\n"), 0o644)

	state = "disabled"
	if ok, where := in.Status(ctx); ok || !strings.Contains(where, "disabled") || !strings.Contains(where, "daemon install") {
		t.Fatalf("disabled unit: %v %q", ok, where)
	}
	state = "enabled"
	if ok, where := in.Status(ctx); !ok || where != path {
		t.Fatalf("enabled unit: %v %q", ok, where)
	}
	if got := strings.Join(calls[len(calls)-1], " "); got != "--user is-enabled "+unitName {
		t.Fatalf("systemctl %s", got)
	}
	systemdAvailable = func() bool { return false }
	if ok, where := in.Status(ctx); ok || !strings.Contains(where, "no systemd") {
		t.Fatalf("no systemd: %v %q", ok, where)
	}
}
