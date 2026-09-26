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

// AppSelfUpdate updates the desktop app — and the CLI bundled next to it —
// from the latest release, then quits (restarting it on macOS/Windows).
//
// The installer lives in the CLI (`monoagentcli update --app`,
// internal/appupdate): it verifies every asset against SHA256SUMS.txt and
// swaps the files all-or-nothing. The app only names its own executable,
// relays the progress as "update:progress" and quits once it is installed.
// The CLI that runs is usually the bundled one, which replaces itself; that
// is safe because it keeps running from the old file until it exits.
func (a *App) AppSelfUpdate() UpdateResult {
	exe, err := os.Executable()
	if err != nil {
		return UpdateResult{Error: "cannot determine executable path"}
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return UpdateResult{Error: err.Error()}
	}
	return runAppUpdate(a.ctx, cliBin, exe, version,
		func(msg string) { runtime.EventsEmit(a.ctx, "update:progress", msg) },
		func() {
			go func() {
				time.Sleep(300 * time.Millisecond)
				runtime.Quit(a.ctx)
			}()
		})
}

// appUpdateTimeout bounds `update --app` (a release download).
const appUpdateTimeout = 15 * time.Minute

// cliAppUpdate is `monoagentcli --json update --app` on stdout.
type cliAppUpdate struct {
	Success    bool   `json:"success"`
	UpToDate   bool   `json:"up_to_date"`
	NewVersion string `json:"new_version"`
	Error      string `json:"error"`
}

// runAppUpdate runs `monoagentcli --json update --app <exe> --current <v>`,
// forwards each NDJSON progress line on its stderr through progress and
// calls quit after a successful install (not when already up to date).
func runAppUpdate(ctx context.Context, cliBin, exe, current string, progress func(string), quit func()) UpdateResult {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, appUpdateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBin, "--json", "update", "--app", exe, "--current", current)
	hideWindow(cmd)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return UpdateResult{Error: err.Error()}
	}
	if err := cmd.Start(); err != nil {
		return UpdateResult{Error: fmt.Sprintf("run %s: %v", cliBin, err)}
	}
	// Anything on stderr that isn't a progress record is kept for the
	// error message of a CLI that fails without a result.
	var other []string
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev struct{ Kind, Message string }
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Kind == "line" {
			progress(ev.Message)
			continue
		}
		if line := strings.TrimSpace(sc.Text()); line != "" {
			other = append(other, line)
		}
	}
	runErr := cmd.Wait()

	var res cliAppUpdate
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &res); err != nil {
		msg := strings.Join(other, "\n")
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		if msg == "" {
			msg = "no result"
		}
		return UpdateResult{Error: "app update: " + msg}
	}
	if !res.Success {
		if res.Error == "" {
			res.Error = "app update failed"
			if runErr != nil {
				res.Error += ": " + runErr.Error()
			}
		}
		return UpdateResult{Error: res.Error}
	}
	if runErr != nil {
		return UpdateResult{Error: fmt.Sprintf("app update: %v", runErr)}
	}
	if res.UpToDate {
		return UpdateResult{Success: true, UpToDate: true, NewVersion: current}
	}
	quit()
	return UpdateResult{Success: true, NewVersion: res.NewVersion}
}
