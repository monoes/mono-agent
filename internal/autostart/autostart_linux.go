//go:build linux

package autostart

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

func newPlatformInstaller() Installer { return linuxInstaller{} }

type linuxInstaller struct{}

const unitName = "monoagent-daemon.service"

const linuxUnitTemplate = `[Unit]
Description=MonoAgent daemon (workflow engine + extension bridge)
After=default.target

[Service]
ExecStart={{.Exe}} daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

// renderUnit returns the unit file for the binary at exe. The path is
// quoted for systemd, which splits ExecStart on spaces and expands % and $:
// a binary under a folder with a space in its name would otherwise not run.
func renderUnit(exe string) (string, error) {
	var b strings.Builder
	tmpl := template.Must(template.New("unit").Parse(linuxUnitTemplate))
	if err := tmpl.Execute(&b, struct{ Exe string }{systemdQuote(exe)}); err != nil {
		return "", err
	}
	return b.String(), nil
}

// systemdQuote quotes one ExecStart word: backslashes and double quotes
// are escaped inside the quotes, % becomes %% (specifier) and $ becomes $$
// (variable expansion).
func systemdQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s)
	return `"` + s + `"`
}

func unitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", unitName), nil
}

// systemdAvailable reports whether this system is actually running under
// systemd. Some of the Linux binaries this project ships land on minimal or
// non-systemd distros; failing here with a clear message beats a confusing
// "systemctl: command not found" or a silent no-op.
func systemdAvailable() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

func (linuxInstaller) Install(ctx context.Context) (Result, error) {
	if !systemdAvailable() {
		return Result{}, fmt.Errorf(
			"no systemd user session detected (/run/systemd/system missing) — `daemon install` needs " +
				"systemd --user; on a system without it, run `monoagentcli daemon` through whatever " +
				"init/supervisor this machine uses instead")
	}
	exe, err := executablePath()
	if err != nil {
		return Result{}, err
	}
	path, err := unitPath()
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	unit, err := renderUnit(exe)
	if err != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, err)
	}

	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", unitName},
	} {
		cmd := exec.CommandContext(ctx, "systemctl", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return Result{}, fmt.Errorf("systemctl %v: %w: %s", args, err, string(out))
		}
	}

	return Result{Description: fmt.Sprintf(
		"Installed %s — starts at your next login, restarts on failure. Logs: journalctl --user -u %s -f. "+
			"Without also running `loginctl enable-linger %s` (needs privileges this command doesn't "+
			"assume) it starts at login rather than at boot before any login.",
		path, unitName, os.Getenv("USER"),
	)}, nil
}

func (linuxInstaller) Uninstall(ctx context.Context) error {
	// Ignore the error: this fails harmlessly when nothing is enabled, the
	// common case when uninstall runs twice or after a partial install.
	_ = exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", unitName).Run()
	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()
	return nil
}
