package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// ─────────────────────────────────────────────────────────────────────────────
// Orgs (monomind Org Runtime v2, via the Agent Exec Protocol's org surface)
//
// Same doctrine as the agent chat bindings in app_ai.go: this file never
// imports internal/monomind — every call shells out to `monoagentcli org
// <sub> ...`, whose stdout JSON is the UI contract. The Wails methods here
// return that JSON verbatim as a string; the frontend parses it (same
// pattern as ScanAgentRuntimes/StreamAgentChat).
//
// Per-profile scoping: `monoagentcli org` resolves org state under
// `--project <root>/.monomind/orgs/` (see cmd/monoagentcli/org.go), NOT via
// the subprocess's cwd — so scoping to the active profile means passing
// `--project <profile root>`, not setting cmd.Dir. orgProjectRoot below
// resolves that root and defensively ensures the folder exists first
// (profiledir.EnsureLayout), falling back to the CLI's own default project
// root (unscoped, today's global behavior) if that fails.
// ─────────────────────────────────────────────────────────────────────────────

// orgCLITimeout bounds one-shot org observe/action calls from the UI.
const orgCLITimeout = 60 * time.Second

// orgProjectRoot resolves the active profile's project root for org state
// (`<root>/.monomind/orgs/`) and ensures the folder layout exists. Returns
// "" if the profile's layout can't be created, so callers can fall back to
// the CLI's default (unscoped) project root rather than failing outright.
func (a *App) orgProjectRoot() string {
	profileID := a.getActiveProfileID()
	if profileID == "" {
		return ""
	}
	if err := profiledir.EnsureLayout(a.db, profileID); err != nil {
		a.emitLog("ORG", "WARN", fmt.Sprintf("could not prepare profile folder for %q: %v (falling back to default org project root)", profileID, err))
		return ""
	}
	return profiledir.Root(a.db, profileID)
}

// runOrgCLI runs `monoagentcli org <args...>` and returns its stdout JSON
// verbatim, or an {"error":"..."} payload on failure (aiError shape,
// app_ai.go) so the frontend's existing error-guard idiom applies unchanged.
func (a *App) runOrgCLI(args ...string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	ctx, cancel := context.WithTimeout(a.ctx, orgCLITimeout)
	defer cancel()
	fullArgs := []string{"org"}
	projectRoot := a.orgProjectRoot()
	if projectRoot != "" {
		fullArgs = append(fullArgs, "--project", projectRoot)
	}
	fullArgs = append(fullArgs, args...)
	logSuffix := ""
	if projectRoot != "" {
		logSuffix = fmt.Sprintf(" (project: %s)", projectRoot)
	}
	a.emitLog("ORG", "INFO", fmt.Sprintf("$ %s %s%s", cliBin, strings.Join(fullArgs, " "), logSuffix))
	startedAt := time.Now()
	cmd := exec.CommandContext(ctx, cliBin, fullArgs...)
	out, err := cmd.Output()
	elapsed := time.Since(startedAt).Round(time.Millisecond)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			stderr := strings.TrimSpace(string(ee.Stderr))
			a.emitLog("ORG", "ERROR", fmt.Sprintf("org %s failed after %s: %s", strings.Join(args, " "), elapsed, stderr))
			return aiError(fmt.Errorf("%s", stderr))
		}
		a.emitLog("ORG", "ERROR", fmt.Sprintf("org %s failed after %s: %v", strings.Join(args, " "), elapsed, err))
		return aiError(err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		a.emitLog("ORG", "WARN", fmt.Sprintf("org %s returned empty output after %s", strings.Join(args, " "), elapsed))
		return aiError(fmt.Errorf("org %s: empty output", strings.Join(args, " ")))
	}
	a.emitLog("ORG", "INFO", fmt.Sprintf("org %s finished in %s", strings.Join(args, " "), elapsed))
	return trimmed
}

func (a *App) ListOrgs() string {
	return a.runOrgCLI("list")
}

func (a *App) GetOrgStatus(name string) string {
	if name == "" {
		return a.runOrgCLI("status")
	}
	return a.runOrgCLI("status", name)
}

// run, when non-empty, scopes to that specific run id instead of
// monomind's own default (the most recent run).
func (a *App) GetOrgLogs(name, run string) string {
	if run != "" {
		return a.runOrgCLI("logs", name, "--run", run)
	}
	return a.runOrgCLI("logs", name)
}

// run is ignored when all=true (report --all already covers every run).
func (a *App) GetOrgReport(name string, all bool, run string) string {
	if all {
		return a.runOrgCLI("report", name, "--all")
	}
	if run != "" {
		return a.runOrgCLI("report", name, "--run", run)
	}
	return a.runOrgCLI("report", name)
}

func (a *App) GetOrgCosts(name, run string) string {
	if run != "" {
		return a.runOrgCLI("costs", name, "--run", run)
	}
	return a.runOrgCLI("costs", name)
}

func (a *App) GetOrgFlow(name, run string) string {
	if run != "" {
		return a.runOrgCLI("flow", name, "--run", run)
	}
	return a.runOrgCLI("flow", name)
}

func (a *App) GetOrgQuestions(name string) string {
	return a.runOrgCLI("questions", name)
}

// GetOrgApprovals returns pending tool/action approval requests — the
// queue that actually gates Bash/WebFetch/WebSearch/org_complete, distinct
// from and not resolved by GetOrgQuestions/GetOrgGates.
func (a *App) GetOrgApprovals(name string) string {
	return a.runOrgCLI("approvals", name)
}

func (a *App) GetOrgGates(name string) string {
	return a.runOrgCLI("gates", name)
}

func (a *App) GetOrgDecisions(name, run string) string {
	if run != "" {
		return a.runOrgCLI("decisions", name, "--run", run)
	}
	return a.runOrgCLI("decisions", name)
}

func (a *App) GetOrgMemoryStats(name string) string {
	return a.runOrgCLI("memory", name)
}

func (a *App) AnswerOrgQuestion(name, questionID, answer string) string {
	return a.runOrgCLI("answer", name, questionID, answer)
}

func (a *App) ApproveOrgAction(name, role, action string) string {
	return a.runOrgCLI("approve", name, role, action)
}

func (a *App) DenyOrgAction(name, role, action string) string {
	return a.runOrgCLI("deny", name, role, action)
}

func (a *App) GateApproveOrgAction(name, gateID, resolution string) string {
	if resolution == "" {
		return a.runOrgCLI("gate-approve", name, gateID)
	}
	return a.runOrgCLI("gate-approve", name, gateID, resolution)
}

func (a *App) GateRejectOrgAction(name, gateID, resolution string) string {
	if resolution == "" {
		return a.runOrgCLI("gate-reject", name, gateID)
	}
	return a.runOrgCLI("gate-reject", name, gateID, resolution)
}

// StreamOrgEvents starts a live tail of an org's bus event log. Each event
// line is re-emitted as a Wails "org:event" event {orgName, event}; the
// subprocess is tracked the same way agent chat is (a.runningCmds, keyed so
// a new stream for the same org supersedes the previous one) and killed as
// a process group on Stop/supersede/shutdown — matching StreamAgentChat.
func (a *App) StreamOrgEvents(orgName string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	eventsArgs := []string{"org"}
	projectRoot := a.orgProjectRoot()
	logSuffix := ""
	if projectRoot != "" {
		eventsArgs = append(eventsArgs, "--project", projectRoot)
		logSuffix = fmt.Sprintf(" (project: %s)", projectRoot)
	}
	eventsArgs = append(eventsArgs, "events", orgName, "--follow")
	a.emitLog("ORG", "INFO", fmt.Sprintf("$ %s %s%s", cliBin, strings.Join(eventsArgs, " "), logSuffix))
	cmd := exec.Command(cliBin, eventsArgs...)
	setChatProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return aiError(err)
	}
	cmd.Stderr = a.chatLogWriter()
	if err := cmd.Start(); err != nil {
		a.emitLog("ORG", "ERROR", fmt.Sprintf("org events failed to start: %v", err))
		return aiError(fmt.Errorf("start org events: %w", err))
	}

	key := "orgevents:" + orgName
	a.runningMu.Lock()
	if prev, ok := a.runningCmds[key]; ok {
		killChatProcessGroup(prev)
		delete(a.runningCmds, key)
	}
	a.runningCmds[key] = cmd
	a.runningMu.Unlock()

	go func() {
		defer func() {
			a.runningMu.Lock()
			delete(a.runningCmds, key)
			a.runningMu.Unlock()
		}()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			runtime.EventsEmit(a.ctx, "org:event", map[string]interface{}{
				"orgName": orgName,
				"event":   json.RawMessage(append([]byte(nil), line...)),
			})
		}
		_ = cmd.Wait()
		runtime.EventsEmit(a.ctx, "org:eventsClosed", map[string]interface{}{"orgName": orgName})
	}()
	return `{"ok":true}`
}

// StopOrgEvents kills the in-flight org event tail subprocess for an org.
func (a *App) StopOrgEvents(orgName string) string {
	key := "orgevents:" + orgName
	a.runningMu.Lock()
	cmd, ok := a.runningCmds[key]
	if ok {
		delete(a.runningCmds, key)
	}
	a.runningMu.Unlock()
	if !ok {
		return `{"ok":false,"error":"no active event stream"}`
	}
	killChatProcessGroup(cmd)
	return `{"ok":true}`
}

// RunOrg starts an org run (`monoagentcli org run <name> --task <task>`) as
// a detached, tracked subprocess — mirroring StreamOrgEvents rather than
// runOrgCLI, since a real run can block for the org's entire lifetime
// (minutes to hours) unless a live `org serve` daemon hands it off almost
// immediately; either way the UI can't wait synchronously for this call to
// return. Progress is observable via the existing Logs page (stdout/stderr
// piped through chatLogWriter) and via the org's own live event tail
// (StreamOrgEvents) if the caller is also subscribed to that. Emits
// "org:runStatus" so any UI can track running/stopped/error without
// polling. There is no companion StopOrg — not needed yet.
func (a *App) RunOrg(orgName, task string) string {
	if orgName == "" {
		return aiError(fmt.Errorf("org name required"))
	}
	key := "orgrun:" + orgName
	a.runningMu.Lock()
	if _, ok := a.runningCmds[key]; ok {
		a.runningMu.Unlock()
		return aiError(fmt.Errorf("org %q is already running", orgName))
	}
	a.runningMu.Unlock()

	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	runArgs := []string{"org"}
	projectRoot := a.orgProjectRoot()
	logSuffix := ""
	if projectRoot != "" {
		runArgs = append(runArgs, "--project", projectRoot)
		logSuffix = fmt.Sprintf(" (project: %s)", projectRoot)
	}
	runArgs = append(runArgs, "run", orgName)
	if task != "" {
		runArgs = append(runArgs, "--task", task)
	}
	a.emitLog("ORG", "INFO", fmt.Sprintf("$ %s %s%s", cliBin, strings.Join(runArgs, " "), logSuffix))
	cmd := exec.Command(cliBin, runArgs...)
	setChatProcessGroup(cmd)
	stderrTail := &tailCapture{}
	cmd.Stdout = a.chatLogWriter()
	cmd.Stderr = io.MultiWriter(a.chatLogWriter(), stderrTail)
	if err := cmd.Start(); err != nil {
		a.emitLog("ORG", "ERROR", fmt.Sprintf("org run failed to start: %v", err))
		return aiError(fmt.Errorf("start org run: %w", err))
	}

	a.runningMu.Lock()
	a.runningCmds[key] = cmd
	a.runningMu.Unlock()

	runtime.EventsEmit(a.ctx, "org:runStatus", map[string]interface{}{"orgName": orgName, "status": "running"})

	go func() {
		waitErr := cmd.Wait()
		a.runningMu.Lock()
		delete(a.runningCmds, key)
		a.runningMu.Unlock()
		status := "stopped"
		message := ""
		if waitErr != nil {
			status = "error"
			message = stderrTail.String()
			if message == "" {
				message = waitErr.Error()
			}
			a.emitLog("ORG", "ERROR", fmt.Sprintf("org run %s exited: %s", orgName, message))
		} else {
			a.emitLog("ORG", "INFO", fmt.Sprintf("org run %s finished", orgName))
		}
		runtime.EventsEmit(a.ctx, "org:runStatus", map[string]interface{}{"orgName": orgName, "status": status, "message": message})
	}()

	return `{"ok":true}`
}

// tailCapture keeps the last few KB written to it — used to capture a
// failed org run's stderr so the "error" org:runStatus event can carry the
// real reason instead of a bare "exited with an error" (the process's exit
// code alone tells the UI nothing actionable).
type tailCapture struct {
	mu  sync.Mutex
	buf []byte
}

const tailCaptureMax = 4000

func (t *tailCapture) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > tailCaptureMax {
		t.buf = t.buf[len(t.buf)-tailCaptureMax:]
	}
	return len(b), nil
}

func (t *tailCapture) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}
