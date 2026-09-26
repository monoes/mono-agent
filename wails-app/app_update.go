package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// appUpdateTimeout bounds the whole download + verify + install.
const appUpdateTimeout = 15 * time.Minute

// AppSelfUpdate updates the desktop app through the CLI
// (`monoagentcli update --app <this exe> --current <version>`): the CLI
// downloads the release asset, verifies it against SHA256SUMS.txt and
// installs it (on Linux also the bundled CLI next to the app). Progress is
// forwarded as "update:progress"; after a successful install the app quits,
// and on macOS/Windows the CLI's detached script starts it again.
func (a *App) AppSelfUpdate() UpdateResult {
	exe, err := os.Executable()
	if err != nil {
		return UpdateResult{Error: "cannot determine the app's executable path"}
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	res, err := a.runAppUpdate(exe, func(msg string) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "update:progress", msg)
		}
	})
	if err != nil {
		return UpdateResult{Error: err.Error()}
	}
	if res.Success && !res.UpToDate && a.ctx != nil {
		go func() {
			time.Sleep(300 * time.Millisecond)
			runtime.Quit(a.ctx)
		}()
	}
	return UpdateResult{Success: res.Success, NewVersion: res.NewVersion, Error: res.Error}
}

// cliAppUpdate is `update --app --json`'s stdout.
type cliAppUpdate struct {
	Success    bool   `json:"success"`
	UpToDate   bool   `json:"up_to_date"`
	NewVersion string `json:"new_version"`
	Restart    string `json:"restart"`
	Error      string `json:"error"`
}

// runAppUpdate runs the CLI update, passing each NDJSON progress line on
// stderr to progress, and decodes the result from stdout.
func (a *App) runAppUpdate(exe string, progress func(string)) (cliAppUpdate, error) {
	var res cliAppUpdate
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return res, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, appUpdateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBin, "--json", "update", "--app", exe, "--current", version)
	hideWindow(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return res, err
	}
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return res, err
	}
	var errLines []string
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		var ev struct {
			Kind, Message string
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Kind == "line" {
			progress(ev.Message)
			continue
		}
		if strings.TrimSpace(line) != "" {
			errLines = append(errLines, line)
		}
	}
	runErr := cmd.Wait()
	if err := json.Unmarshal([]byte(stdout.String()), &res); err != nil {
		if runErr != nil {
			return res, fmt.Errorf("update failed: %s", strings.TrimSpace(strings.Join(errLines, "\n")+" "+runErr.Error()))
		}
		return res, fmt.Errorf("update: unreadable CLI output: %v", err)
	}
	return res, nil
}
