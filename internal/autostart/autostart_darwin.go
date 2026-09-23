//go:build darwin

package autostart

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"text/template"
)

func newPlatformInstaller() Installer { return darwinInstaller{} }

type darwinInstaller struct{}

const darwinPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Exe}}</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogDir}}/daemon.log</string>
	<key>StandardErrorPath</key>
	<string>{{.LogDir}}/daemon.err.log</string>
</dict>
</plist>
`

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// launchdDomain is the per-user GUI domain launchctl's modern bootstrap/
// bootout subcommands operate on — the successor to `load -w`/`unload -w`,
// which Apple deprecated and which behaves inconsistently for agents
// started outside a full login (e.g. over SSH).
func launchdDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func (darwinInstaller) Install(ctx context.Context) (Result, error) {
	exe, err := executablePath()
	if err != nil {
		return Result{}, err
	}
	logs, err := logDir()
	if err != nil {
		return Result{}, err
	}
	path, err := plistPath()
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	tmpl := template.Must(template.New("plist").Parse(darwinPlistTemplate))
	f, err := os.Create(path)
	if err != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, err)
	}
	execErr := tmpl.Execute(f, struct{ Label, Exe, LogDir string }{Label, exe, logs})
	closeErr := f.Close()
	if execErr != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, execErr)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("write %s: %w", path, closeErr)
	}

	domain := launchdDomain()
	// bootout first and ignore its error: it fails (harmlessly) whenever
	// nothing is loaded yet, which is the common case on a first install,
	// and re-running bootstrap over an already-loaded label otherwise
	// fails with "service already loaded".
	_ = exec.CommandContext(ctx, "launchctl", "bootout", domain+"/"+Label).Run()
	bootstrap := exec.CommandContext(ctx, "launchctl", "bootstrap", domain, path)
	if out, err := bootstrap.CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("launchctl bootstrap: %w: %s", err, string(out))
	}

	return Result{Description: fmt.Sprintf(
		"Installed %s — starts at login, restarts if it exits. Logs: %s/daemon.log (and daemon.err.log). "+
			"Inspect with: launchctl print %s/%s", path, logs, domain, Label,
	)}, nil
}

func (darwinInstaller) Uninstall(ctx context.Context) error {
	path, err := plistPath()
	if err != nil {
		return err
	}
	domain := launchdDomain()
	_ = exec.CommandContext(ctx, "launchctl", "bootout", domain+"/"+Label).Run()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
