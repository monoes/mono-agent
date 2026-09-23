// Package autostart registers monoagentcli's own daemon to start
// automatically at login, on whichever OS it's running on — doing once,
// per install, what `daemon`'s own docs used to just tell people to go set
// up themselves ("run as a background/system service").
package autostart

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Label identifies the installed service across every backend: the launchd
// job label, the systemd unit's base name, and the Windows scheduled task
// name all derive from it, so install and uninstall always agree on what
// they're touching.
const Label = "com.monoagent.daemon"

// Installer registers and removes the per-user auto-start entry. Each OS
// implements exactly one, selected by build tag (autostart_darwin.go,
// autostart_linux.go, autostart_windows.go) so a cross-compiled binary
// never references another platform's service manager.
type Installer interface {
	// Install writes the service definition and starts it now, replacing
	// any previous registration under Label. Safe to call again over an
	// existing install.
	Install(ctx context.Context) (Result, error)
	// Uninstall stops the service and removes its definition. Safe to call
	// when nothing is installed.
	Uninstall(ctx context.Context) error
}

// Result describes what Install did, for the CLI to print.
type Result struct {
	// Description is a one-line human summary of where the registration
	// lives and how to inspect it further.
	Description string
}

// New returns this platform's Installer.
func New() Installer {
	return newPlatformInstaller()
}

// executablePath resolves the absolute, symlink-free path to the running
// binary. Every backend needs this: a launchd/systemd/Scheduled Task
// definition has no shell and no PATH to find "monoagentcli" by name.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve this binary's path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil // best effort: the unresolved path still works almost always
}

// logDir returns ~/.monoagent/logs, creating it if needed — where every
// backend that can redirect stdout/stderr points the installed service.
func logDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".monoagent", "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}
