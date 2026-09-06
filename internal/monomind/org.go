package monomind

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Org observe/action commands (protocol §7): thin proxies over
// `monomind org <sub> [<name>] --format json`. Every subcommand resolves
// its project by cwd (§7.1), so callers pass projectRoot explicitly — this
// client manages multiple project roots, unlike the CLI's own cwd.
//
// Read-only/action results are returned as json.RawMessage rather than
// decoded into typed structs: the org JSON shapes are still evolving on the
// monomind side and callers here (the CLI, then the Wails bindings, then
// the frontend) only need to pass the payload through, not manipulate it in
// Go. This avoids silently dropping fields we didn't anticipate.

// orgTimeout bounds one-shot org observe/action calls.
var orgTimeout = 60 * time.Second

// runOrgJSON runs `monomind org <args...> --format json` with cwd=projectRoot
// and returns the raw stdout payload.
func runOrgJSON(ctx context.Context, projectRoot string, args ...string) (json.RawMessage, error) {
	bin, _, err := Ensure(ctx)
	if err != nil {
		return nil, err
	}
	full := append(append([]string{"org"}, args...), "--format", "json")

	cctx, cancel := context.WithTimeout(ctx, orgTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, full...)
	cmd.Dir = projectRoot

	out, err := cmd.Output()
	if err != nil {
		return nil, orgCommandError(args, err)
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("monomind org %s: empty output", strings.Join(args, " "))
	}
	return json.RawMessage(trimmed), nil
}

// orgCommandError extracts the actionable message from a failed org
// subprocess. Exit codes are not reliably distinguishable between usage and
// runtime errors (protocol §7.1 caveat verified against org.ts), so this
// always surfaces stderr text rather than branching on exit status.
func orgCommandError(args []string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := strings.TrimSpace(string(ee.Stderr))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("monomind org %s: %s", strings.Join(args, " "), msg)
	}
	return fmt.Errorf("monomind org %s: %w", strings.Join(args, " "), err)
}

// runOrgText runs `monomind org <args...>` whose output is human text, not
// protocol JSON (validate/reload — neither takes --format json). Mirrors
// runOrgJSON's arg convention: callers pass the subcommand args without the
// "org" prefix, which is added internally. Returns the trimmed
// stdout+stderr text and a non-nil error when the exit code is non-zero,
// using orgCommandError's stderr-extraction pattern already established for
// runOrgJSON.
func runOrgText(ctx context.Context, projectRoot string, args ...string) (string, error) {
	bin, _, err := Ensure(ctx)
	if err != nil {
		return "", err
	}
	full := append([]string{"org"}, args...)

	cctx, cancel := context.WithTimeout(ctx, orgTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, full...)
	cmd.Dir = projectRoot

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", orgCommandError(args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// OrgValidate runs `monomind org validate <name>` and returns its output
// text. A non-nil error means validation failed (or the subprocess itself
// failed to run) — err's message is the actionable text from the CLI.
func OrgValidate(ctx context.Context, projectRoot, name string) (string, error) {
	return runOrgText(ctx, projectRoot, "validate", name)
}

// OrgReload signals a running org's daemon to pick up config changes
// without a full restart (writes the CLI's own `reload` sentinel file).
func OrgReload(ctx context.Context, projectRoot, name string) (string, error) {
	return runOrgText(ctx, projectRoot, "reload", name)
}

// OrgList returns every org in the project (`org list`).
func OrgList(ctx context.Context, projectRoot string) (json.RawMessage, error) {
	return runOrgJSON(ctx, projectRoot, "list")
}

// OrgRun starts `monomind org run <name> --yes [--task ...] [--dry-run]`,
// blocking until it returns. Deliberately does NOT go through runOrgJSON:
// that helper caps every call at orgTimeout (60s), but a real run blocks
// for as long as the org takes to finish (minutes to hours) unless a `org
// serve` daemon is already live for the project, in which case it hands
// off and returns almost immediately — either way, the caller's own ctx is
// the only deadline that should apply here. Callers that need a bounded
// wait should poll OrgStatus instead of waiting on this call to return.
func OrgRun(ctx context.Context, projectRoot, name, task string, dryRun bool) (json.RawMessage, error) {
	bin, _, err := Ensure(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{"run", name, "--yes"}
	if task != "" {
		args = append(args, "--task", task)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	full := append(append([]string{"org"}, args...), "--format", "json")

	// Deliberately NOT exec.CommandContext: its default Cancel behavior
	// only kills the direct child on ctx cancellation, not its process
	// group — leaving any agent-CLI grandchild `monomind org run` spawned
	// as an orphan (this can block "minutes to hours" per the doc comment
	// above, so cancellation is the only way most callers ever stop it).
	// setProcessGroup + a manual ctx.Done()/killProcessGroup select mirrors
	// OrgEvents below, the most similar long-running case.
	cmd := exec.Command(bin, full...)
	cmd.Dir = projectRoot
	setProcessGroup(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start monomind org run %s: %w", name, err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd, cmd.Process.Pid)
		<-waitCh
		return nil, ctx.Err()
	case err := <-waitCh:
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return nil, fmt.Errorf("monomind org %s: %s", strings.Join(args, " "), msg)
		}
		trimmed := bytes.TrimSpace(stdout.Bytes())
		if len(trimmed) == 0 {
			return nil, fmt.Errorf("monomind org run %s: empty output", name)
		}
		return json.RawMessage(trimmed), nil
	}
}

// OrgRunStart starts `monomind org run <name> --yes [--task ...]` as a
// detached background process and returns as soon as it's spawned, without
// waiting for it to finish — for callers (the org.run workflow node,
// RunOrg's Wails binding) that poll OrgStatus separately instead of
// blocking on OrgRun's return. The process is reaped in a background
// goroutine so it never becomes a zombie; its exit is otherwise
// unobserved by this function — callers that need to know when it exits
// should poll OrgStatus for closed_by, not rely on this call.
func OrgRunStart(ctx context.Context, projectRoot, name, task string) error {
	bin, _, err := Ensure(ctx)
	if err != nil {
		return err
	}
	args := []string{"org", "run", name, "--yes"}
	if task != "" {
		args = append(args, "--task", task)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = projectRoot
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start monomind org run %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// OrgStatus returns one org's status, or every org's status when name=="".
func OrgStatus(ctx context.Context, projectRoot, name string) (json.RawMessage, error) {
	args := []string{"status"}
	if name != "" {
		args = append(args, name)
	}
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgLogs returns the org's bus event log (`org logs <name>`). There is no
// --tail flag on the live monomind CLI despite the plan doc's claim.
// run, when non-empty, scopes to that specific run id (`--run <id>`)
// instead of monomind's own default of "the most recent run" (org.ts's
// resolveRun).
func OrgLogs(ctx context.Context, projectRoot, name, run string) (json.RawMessage, error) {
	args := []string{"logs", name}
	if run != "" {
		args = append(args, "--run", run)
	}
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgReport returns the org's run report; all=true requests every recorded
// run (`org report <name> --all`) instead of just one. run, when non-empty,
// scopes the non-all case to that specific run id (`--run <id>`) instead of
// monomind's own default of "the most recent run" — ignored when all=true,
// since --all already reports on every run.
func OrgReport(ctx context.Context, projectRoot, name string, all bool, run string) (json.RawMessage, error) {
	args := []string{"report", name}
	if all {
		args = append(args, "--all")
	} else if run != "" {
		args = append(args, "--run", run)
	}
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgCosts returns per-role token/cost totals (`org costs <name>`). run, when
// non-empty, scopes to that specific run id (`--run <id>`) instead of
// monomind's own default of "the most recent run".
func OrgCosts(ctx context.Context, projectRoot, name, run string) (json.RawMessage, error) {
	args := []string{"costs", name}
	if run != "" {
		args = append(args, "--run", run)
	}
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgFlow returns the org's role communication graph (`org flow <name>`).
// run, when non-empty, scopes to that specific run id (`--run <id>`) instead
// of monomind's own default of "the most recent run".
func OrgFlow(ctx context.Context, projectRoot, name, run string) (json.RawMessage, error) {
	args := []string{"flow", name}
	if run != "" {
		args = append(args, "--run", run)
	}
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgQuestions returns pending human-input questions (`org questions <name>`).
func OrgQuestions(ctx context.Context, projectRoot, name string) (json.RawMessage, error) {
	return runOrgJSON(ctx, projectRoot, "questions", name)
}

// OrgApprovals returns pending tool/action approval requests (`org
// approvals <name>`) — the queue checked by checkApproval for
// Bash/WebFetch/WebSearch/org_complete, distinct from and not resolved by
// OrgQuestions/OrgGates.
func OrgApprovals(ctx context.Context, projectRoot, name string) (json.RawMessage, error) {
	return runOrgJSON(ctx, projectRoot, "approvals", name)
}

// OrgGates returns pending decision gates (`org gates <name>`).
func OrgGates(ctx context.Context, projectRoot, name string) (json.RawMessage, error) {
	return runOrgJSON(ctx, projectRoot, "gates", name)
}

// OrgDecisions returns the org's decision trace (`org decisions <name>`).
// run, when non-empty, scopes to that specific run id (`--run <id>`) instead
// of monomind's own default of "the most recent run".
func OrgDecisions(ctx context.Context, projectRoot, name, run string) (json.RawMessage, error) {
	args := []string{"decisions", name}
	if run != "" {
		args = append(args, "--run", run)
	}
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgMemoryStats returns org memory statistics (`org memory <name> stats`).
func OrgMemoryStats(ctx context.Context, projectRoot, name string) (json.RawMessage, error) {
	return runOrgJSON(ctx, projectRoot, "memory", name, "stats")
}

// OrgAnswer answers a pending human-input question
// (`org answer <name> <questionID> <answer...>`).
func OrgAnswer(ctx context.Context, projectRoot, name, questionID, answer string) (json.RawMessage, error) {
	return runOrgJSON(ctx, projectRoot, "answer", name, questionID, answer)
}

// OrgApprove approves a pending tool-approval request
// (`org approve <name> <role> <action>` — role+action, not an id).
func OrgApprove(ctx context.Context, projectRoot, name, role, action string) (json.RawMessage, error) {
	return runOrgJSON(ctx, projectRoot, "approve", name, role, action)
}

// OrgDeny denies a pending tool-approval request (`org deny <name> <role> <action>`).
func OrgDeny(ctx context.Context, projectRoot, name, role, action string) (json.RawMessage, error) {
	return runOrgJSON(ctx, projectRoot, "deny", name, role, action)
}

// OrgGateApprove approves a decision gate
// (`org gate-approve <name> <gateID> [resolution...]`).
func OrgGateApprove(ctx context.Context, projectRoot, name, gateID, resolution string) (json.RawMessage, error) {
	args := []string{"gate-approve", name, gateID}
	if resolution != "" {
		args = append(args, resolution)
	}
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgGateReject rejects a decision gate (`org gate-reject <name> <gateID> [resolution...]`).
func OrgGateReject(ctx context.Context, projectRoot, name, gateID, resolution string) (json.RawMessage, error) {
	args := []string{"gate-reject", name, gateID}
	if resolution != "" {
		args = append(args, resolution)
	}
	return runOrgJSON(ctx, projectRoot, args...)
}

// OrgEventsOptions configures OrgEvents.
type OrgEventsOptions struct {
	Run    string // --run <id>, empty = current run
	Follow bool   // --follow: keep streaming (like tail -f)
	Since  string // --since <eventId|iso>
}

// OrgEvents streams the org's bus.jsonl as NDJSON (`org events <name>`).
// NDJSON is the command's only output mode — no --format flag applies here.
// onLine is invoked once per raw JSON line, in arrival order. OrgEvents
// blocks until the subprocess exits or ctx is cancelled (in which case the
// process group is killed so no `monomind` or agent-CLI grandchild survives
// the caller, matching Exec's cancellation contract).
func OrgEvents(ctx context.Context, projectRoot, name string, opts OrgEventsOptions, onLine func(line []byte)) error {
	bin, _, err := Ensure(ctx)
	if err != nil {
		return err
	}
	args := []string{"org", "events", name}
	if opts.Run != "" {
		args = append(args, "--run", opts.Run)
	}
	if opts.Follow {
		args = append(args, "--follow")
	}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = projectRoot
	setProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start monomind org events: %w", err)
	}

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue
			}
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line)
			onLine(lineCopy)
		}
	}()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd, cmd.Process.Pid)
		<-readerDone
		<-waitCh
		return ctx.Err()
	case err := <-waitCh:
		<-readerDone
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("monomind org events %s: %s", name, msg)
		}
		return nil
	}
}
