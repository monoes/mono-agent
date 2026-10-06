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

	"github.com/monoes/mono-agent/internal/orgdesign"
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
	return runOrgJSONFull(ctx, projectRoot, args, append(append([]string{"org"}, args...), "--format", "json"))
}

// runOrgJSONText is runOrgJSON for a subcommand whose trailing values are
// free text (an answer, a gate's resolution) or ids: cmd is the subcommand
// and its flags, and values go after "--", so a value like "--by=rule" is
// never read as a flag.
func runOrgJSONText(ctx context.Context, projectRoot string, cmd []string, values ...string) (json.RawMessage, error) {
	full := append(append([]string{"org"}, cmd...), "--format", "json", "--")
	return runOrgJSONFull(ctx, projectRoot, append(append([]string(nil), cmd...), values...), append(full, values...))
}

// runOrgJSONFull runs `monomind <full...>`; args names the command in errors.
func runOrgJSONFull(ctx context.Context, projectRoot string, args, full []string) (json.RawMessage, error) {
	bin, err := EnsureIn(ctx, projectRoot)
	if err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, orgTimeout)
	defer cancel()
	cmd := CommandContext(cctx, bin, full...)
	inRoot(cmd, projectRoot)

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
	bin, err := EnsureIn(ctx, projectRoot)
	if err != nil {
		return "", err
	}
	return runOrgTextWith(ctx, bin, projectRoot, args...)
}

// runOrgTextWith is runOrgText through a given monomind binary.
func runOrgTextWith(ctx context.Context, bin, projectRoot string, args ...string) (string, error) {
	out, err := orgTextOutput(ctx, bin, projectRoot, args...)
	return orgTextResult(args, out, err)
}

// orgTextOutput runs `<bin> org <args...>` in projectRoot: its combined
// output and exit error, unformatted.
func orgTextOutput(ctx context.Context, bin, projectRoot string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, orgTimeout)
	defer cancel()
	cmd := CommandContext(cctx, bin, append([]string{"org"}, args...)...)
	inRoot(cmd, projectRoot)
	return cmd.CombinedOutput()
}

// orgTextResult is runOrgTextWith's result from orgTextOutput's.
func orgTextResult(args []string, out []byte, err error) (string, error) {
	if err != nil {
		// CombinedOutput leaves ExitError.Stderr empty, so orgCommandError
		// alone would say only "exit status 1": keep what monomind printed
		// (for validate, the list of problems).
		if msg := strings.TrimSpace(string(out)); msg != "" {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return "", fmt.Errorf("monomind org %s: %s", strings.Join(args, " "), msg)
			}
		}
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
	raw, err := runOrgJSON(ctx, projectRoot, "list")
	if err != nil {
		return nil, err
	}
	return dropUnnamableOrgs(raw), nil
}

// dropUnnamableOrgs removes list items whose name could not belong to an org.
//
// The orgs folder holds more than orgs — a .mcp.json tool config, whatever
// else a user drops beside them — and monomind's own lister takes every
// .json in it, so `org list` offered ".mcp" as an org with no roles. The
// designer then reported it, correctly for an empty config, as having no
// root role; it sorts before every letter, so it was the entry the GUI
// selected by default and the error a user saw constantly while every real
// org was fine. monomind itself refuses to act on such a name ("invalid org
// name"), so listing it can only mislead.
//
// Unparseable or unexpected output is passed through untouched: this filters
// a known bad entry, it does not police the protocol.
func dropUnnamableOrgs(raw json.RawMessage) json.RawMessage {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return raw
	}
	itemsRaw, ok := envelope["items"]
	if !ok {
		return raw
	}
	var items []json.RawMessage
	if err := json.Unmarshal(itemsRaw, &items); err != nil {
		return raw
	}
	kept := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var probe struct {
			Name string `json:"name"`
		}
		// An item we cannot read a name from stays — dropping it would hide
		// an org over a shape we simply did not expect.
		if err := json.Unmarshal(item, &probe); err != nil || probe.Name == "" || orgdesign.ValidOrgName(probe.Name) {
			kept = append(kept, item)
		}
	}
	if len(kept) == len(items) {
		return raw
	}
	filtered, err := json.Marshal(kept)
	if err != nil {
		return raw
	}
	envelope["items"] = filtered
	out, err := json.Marshal(envelope)
	if err != nil {
		return raw
	}
	return out
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
	bin, err := EnsureIn(ctx, projectRoot)
	if err != nil {
		return nil, err
	}
	if err := checkOrgSigned(ctx, projectRoot, name); err != nil {
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
	cmd := Command(bin, full...)
	inRoot(cmd, projectRoot)
	setProcessGroup(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	release, err := startProcessGroup(cmd)
	if err != nil {
		return nil, fmt.Errorf("start monomind org run %s: %w", name, err)
	}
	defer release()

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
			if signatureRefusalReason(msg) == "" && signatureRefusalReason(stdout.String()) != "" {
				// org run prints a signature refusal on stdout.
				msg = strings.TrimSpace(msg + "\n" + stdout.String())
			}
			// ...and a host (R6) or daemon-lock (R1) refusal too, leaving only
			// "[ERROR] org start failed" on stderr.
			if r := asStartRefusal(name, stdout.String()+"\n"+msg); r != nil {
				return nil, r
			}
			return nil, asSignatureRefusal(name, fmt.Errorf("monomind org %s: %s", strings.Join(args, " "), msg))
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
// should poll OrgStatus for closed_by, not rely on this call. Nothing
// here kills it: OrgStop (`monomind org stop`) ends it cooperatively, the
// same on every platform, so startDetached keeps it out of our jobs.
func OrgRunStart(ctx context.Context, projectRoot, name, task string) error {
	if !orgdesign.ValidOrgName(name) {
		return fmt.Errorf("invalid org name %q", name)
	}
	bin, err := EnsureIn(ctx, projectRoot)
	if err != nil {
		return err
	}
	if err := checkOrgSigned(ctx, projectRoot, name); err != nil {
		return err
	}
	args := []string{"org", "run", name, "--yes"}
	if task != "" {
		args = append(args, "--task", task)
	}
	cmd := Command(bin, args...)
	inRoot(cmd, projectRoot)
	// A start monomind refuses (R6/R1) exits at once and says why on its
	// output; keep that (boundedly) to report it instead of a start nobody
	// sees fail.
	capture, err := newStartCapture()
	if err != nil {
		return fmt.Errorf("start monomind org run %s: %w", name, err)
	}
	if err := capture.startDrain(ctx, bin, projectRoot); err != nil {
		capture.close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("start org output capture: %w", err)
	}
	cmd.Stdout, cmd.Stderr = capture.stream, capture.stream
	began := time.Now()
	cmd, err = startDetached(cmd)
	// Only monomind keeps the write end, so its exit ends the drainer.
	capture.stream.Close()
	if err != nil {
		capture.finishDrain()
		capture.close()
		return fmt.Errorf("start monomind org run %s: %w", name, err)
	}
	return watchStart(ctx, cmd, name, capture, func() bool { return runStarted(projectRoot, name, began) })
}

// OrgStatus returns one org's status, or every org's status when name=="".
func OrgStatus(ctx context.Context, projectRoot, name string) (json.RawMessage, error) {
	args := []string{"status"}
	if name != "" {
		args = append(args, name)
	}
	raw, err := runOrgJSON(ctx, projectRoot, args...)
	if err != nil {
		return nil, err
	}
	// `status` with no name is a list too, and carries the same phantoms.
	if name == "" {
		return deadRunsStopped(projectRoot, dropUnnamableOrgs(raw)), nil
	}
	return deadRunsStopped(projectRoot, raw), nil
}

// deadRunsStopped reports a "running" org whose run is dead (OrgRunDead)
// as "stopped", in a one-org status or in the list's items, so the GUI and
// every caller agree with `org summary` (#294, monoes/monomind#573).
func deadRunsStopped(projectRoot string, raw json.RawMessage) json.RawMessage {
	fix := func(item json.RawMessage) (json.RawMessage, bool) {
		var obj map[string]json.RawMessage
		if json.Unmarshal(item, &obj) != nil {
			return item, false
		}
		var name, status string
		_ = json.Unmarshal(obj["name"], &name)
		_ = json.Unmarshal(obj["status"], &status)
		if status != "running" || !orgdesign.ValidOrgName(name) || !OrgRunDead(projectRoot, name) {
			return item, false
		}
		obj["status"] = json.RawMessage(`"stopped"`)
		out, err := json.Marshal(obj)
		if err != nil {
			return item, false
		}
		return out, true
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return raw
	}
	itemsRaw, isList := envelope["items"]
	if !isList {
		out, _ := fix(raw)
		return out
	}
	var items []json.RawMessage
	if json.Unmarshal(itemsRaw, &items) != nil {
		return raw
	}
	changed := false
	for i, item := range items {
		if out, ok := fix(item); ok {
			items[i], changed = out, true
		}
	}
	if !changed {
		return raw
	}
	b, err := json.Marshal(items)
	if err != nil {
		return raw
	}
	envelope["items"] = b
	out, err := json.Marshal(envelope)
	if err != nil {
		return raw
	}
	return out
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

// OrgHumanItemsAll returns every question, approval or gate of the org,
// resolved ones included (`org questions|approvals|gates <name> --all`):
// kind is "questions", "approvals" or "gates". Resolving an item twice is
// only safe when the caller can tell "already resolved" from "unknown".
func OrgHumanItemsAll(ctx context.Context, projectRoot, name, kind string) (json.RawMessage, error) {
	switch kind {
	case "questions", "approvals", "gates":
	default:
		return nil, fmt.Errorf("monomind org: unknown item kind %q", kind)
	}
	return runOrgJSON(ctx, projectRoot, kind, name, "--all")
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
	return runOrgJSONText(ctx, projectRoot, []string{"answer"}, name, questionID, answer)
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
	values := []string{name, gateID}
	if resolution != "" {
		values = append(values, resolution)
	}
	return runOrgJSONText(ctx, projectRoot, []string{"gate-approve"}, values...)
}

// OrgGateReject rejects a decision gate (`org gate-reject <name> <gateID> [resolution...]`).
func OrgGateReject(ctx context.Context, projectRoot, name, gateID, resolution string) (json.RawMessage, error) {
	values := []string{name, gateID}
	if resolution != "" {
		values = append(values, resolution)
	}
	return runOrgJSONText(ctx, projectRoot, []string{"gate-reject"}, values...)
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
	bin, err := EnsureIn(ctx, projectRoot)
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

	cmd := Command(bin, args...)
	inRoot(cmd, projectRoot)
	setProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	release, err := startProcessGroup(cmd)
	if err != nil {
		return fmt.Errorf("start monomind org events: %w", err)
	}
	defer release()

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
	go func() {
		// Wait closes the stdout pipe as soon as the process exits, and the
		// os/exec docs are explicit that calling it before every read has
		// completed is incorrect. Running it concurrently with the scanner
		// meant a fast-exiting `org events` could have its last lines closed
		// out from under the reader: the caller silently saw fewer events, or
		// none. It showed up as a CI-only test failure ("delivered 0 lines,
		// want 2") because losing the race needs the machine to be busy.
		<-readerDone
		waitCh <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd, cmd.Process.Pid)
		<-waitCh // the reader has already finished; waitCh implies readerDone
		return ctx.Err()
	case err := <-waitCh:
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
