//go:build windows

package autostart

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func newPlatformInstaller() Installer { return windowsInstaller{} }

type windowsInstaller struct{}

const taskName = "MonoAgentDaemon"

func (windowsInstaller) Install(ctx context.Context) (Result, error) {
	exe, err := executablePath()
	if err != nil {
		return Result{}, err
	}
	logs, err := logDir()
	if err != nil {
		return Result{}, err
	}
	logPath := filepath.Join(logs, "daemon.log")

	// schtasks has no native stdout/stderr redirection (unlike the plist's
	// StandardOutPath or systemd's journal), so the task runs a cmd
	// wrapper that appends both streams to a file instead. /rl limited
	// avoids requiring admin rights for a per-user task.
	trArg := fmt.Sprintf(`cmd /c ""%s" daemon >> "%s" 2>&1"`, exe, logPath)

	cmd := exec.CommandContext(ctx, "schtasks", "/create", "/tn", taskName,
		"/sc", "onlogon", "/rl", "limited", "/f", "/tr", trArg)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("schtasks /create: %w: %s", err, string(out))
	}

	// /create only schedules it for the next logon; start it now too so
	// install has the same "running immediately" effect on every platform.
	_ = exec.CommandContext(ctx, "schtasks", "/run", "/tn", taskName).Run()

	return Result{Description: fmt.Sprintf(
		"Installed scheduled task %q — starts at your next logon. Logs: %s. Inspect with: schtasks /query /tn %s /v",
		taskName, logPath, taskName,
	)}, nil
}

func (windowsInstaller) Uninstall(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "schtasks", "/delete", "/tn", taskName, "/f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out))
		if strings.Contains(msg, "cannot find") || strings.Contains(msg, "does not exist") {
			return nil // nothing was installed — uninstall is a no-op, not an error
		}
		return fmt.Errorf("schtasks /delete: %w: %s", err, string(out))
	}
	return nil
}
