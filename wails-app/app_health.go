package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// Settings › System health. Everything is `monoagentcli doctor` — this file
// only runs it and relays its JSON (docs/plans/2026-09-24-setup-and-health-
// check.md §5). The one thing the GUI answers itself is "is there a CLI at
// all", since it can't ask a CLI that isn't there.
// ─────────────────────────────────────────────────────────────────────────────

const (
	healthCheckTimeout    = 4 * time.Minute  // runtime scan + monomind's own checks
	healthProjectsTimeout = 12 * time.Minute // monomind doctor per project
	healthFixTimeout      = 20 * time.Minute // installs (Node, monomind, runtimes)
)

// healthArgs builds `[--profile P] --json doctor [--deep] [--projects]`.
func healthArgs(profileID string, deep, projects bool) []string {
	args := []string{}
	if profileID != "" {
		args = append(args, "--profile", profileID)
	}
	args = append(args, "--json", "doctor")
	if deep {
		args = append(args, "--deep")
	}
	if projects {
		args = append(args, "--projects")
	}
	return args
}

// healthFixArgs builds `[--profile P] --json doctor fix <id>`.
func healthFixArgs(profileID, fixID string) []string {
	args := []string{}
	if profileID != "" {
		args = append(args, "--profile", profileID)
	}
	// "--" so a fix id can never be read as a flag.
	return append(args, "--json", "doctor", "fix", "--", fixID)
}

// healthReportJSON returns the doctor report verbatim. doctor exits 1 when a
// required check fails but still prints the full report, so a report on
// stdout wins over the exit status.
func healthReportJSON(cliBin string, stdout []byte, runErr error) string {
	trimmed := strings.TrimSpace(string(stdout))
	var probe struct {
		V int `json:"v"`
	}
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &probe) == nil && probe.V > 0 {
		return trimmed
	}
	return cliResultJSON(cliBin, stdout, runErr)
}

// RunHealthCheck runs every check (deep adds network checks, projects adds
// monomind's checks per project) and returns the doctor --json report, or
// {"error", "cli_missing"} when there is no monoagentcli to ask.
func (a *App) RunHealthCheck(deep, projects bool) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": err.Error(), "cli_missing": true})
		return string(b)
	}
	timeout := healthCheckTimeout
	if projects {
		timeout = healthProjectsTimeout
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBin, healthArgs(a.getActiveProfileID(), deep, projects)...)
	hideWindow(cmd)
	stopGracefully(cmd)
	out, runErr := cmd.Output()
	return healthReportJSON(cliBin, out, runErr)
}

// healthFixEvent is one progress event for the frontend: the CLI's own
// NDJSON line ({"kind":"line"|"done"|"error","message"}) tagged with the fix.
type healthFixEvent struct {
	FixID   string `json:"fix_id"`
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
}

// RunHealthFix applies one fix in the background, relaying progress as
// "health:fixProgress" events, and returns at once. The frontend has
// already asked the user for confirm fixes.
func (a *App) RunHealthFix(fixID string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	// One run per fix: two `doctor fix` processes for the same fix would
	// start two daemons or two npm installs into one global folder, and both
	// would report on the same fix id.
	if _, busy := runningFixes.LoadOrStore(fixID, true); busy {
		return aiError(fmt.Errorf("%s is already running", fixID))
	}
	args := healthFixArgs(a.getActiveProfileID(), fixID)
	go func() {
		defer runningFixes.Delete(fixID)
		a.streamHealthFix(cliBin, fixID, args)
	}()
	return `{"ok":true}`
}

// runningFixes holds the ids of the fixes running now.
var runningFixes sync.Map

// stopGracefully makes the context's cancel (a timeout, the app closing)
// ask the CLI to stop instead of killing it outright, so it can end the
// installers it started; WaitDelay then kills it, and stops a child that
// still holds the output pipe from blocking the read.
func stopGracefully(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return terminateCLI(cmd) }
	cmd.WaitDelay = 15 * time.Second
}

func (a *App) streamHealthFix(cliBin, fixID string, args []string) {
	emit := func(ev healthFixEvent) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "health:fixProgress", ev)
		}
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, healthFixTimeout)
	defer cancel()
	a.emitLog("HEALTH", "INFO", fmt.Sprintf("$ %s %s", cliBin, strings.Join(args, " ")))
	cmd := exec.CommandContext(ctx, cliBin, args...)
	hideWindow(cmd)
	stopGracefully(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emit(healthFixEvent{FixID: fixID, Kind: "error", Message: err.Error()})
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		emit(healthFixEvent{FixID: fixID, Kind: "error", Message: err.Error()})
		return
	}
	finished := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev healthFixEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Kind == "" {
			ev = healthFixEvent{Kind: "line", Message: sc.Text()}
		}
		ev.FixID = fixID
		if ev.Kind == "done" || ev.Kind == "error" {
			finished = true
		}
		emit(ev)
	}
	if err := sc.Err(); err != nil {
		// A line too long for the scanner: keep draining so the CLI isn't
		// blocked writing to a full pipe until the timeout.
		emit(healthFixEvent{FixID: fixID, Kind: "line", Message: "(output line too long to show)"})
		_, _ = io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()
	if !finished {
		// The CLI died without its final event (killed, timed out, crashed).
		msg := "the fix stopped without reporting a result"
		if waitErr != nil {
			msg = waitErr.Error()
		}
		if s := strings.TrimSpace(stderr.String()); s != "" {
			msg += ": " + s
		}
		emit(healthFixEvent{FixID: fixID, Kind: "error", Message: msg})
	}
	level := "INFO"
	if waitErr != nil {
		level = "ERROR"
	}
	a.emitLog("HEALTH", level, fmt.Sprintf("doctor fix %s finished", fixID))
}
