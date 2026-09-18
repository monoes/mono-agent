package monomind

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// ToolHandler executes one bridged tool call (protocol §4). The returned
// string is what the agent sees; an error becomes an ok:false frame.
type ToolHandler func(ctx context.Context, name string, args json.RawMessage) (string, error)

// ExecOptions configures one `monomind agent exec` turn (protocol §3.1).
type ExecOptions struct {
	Runtime string
	Prompt  string
	Model   string
	Cwd     string
	Resume  string
	// SystemPrompt, when set, is written to a temp file and passed via
	// --system-file (avoids argv limits).
	SystemPrompt string
	// Tools enables the stdio bridge (§4); empty passes --tools none
	// explicitly so the turn never inherits monomind's default toolset.
	Tools []ToolSpec
	// OnToolCall executes bridged tool calls; required when Tools is set.
	OnToolCall ToolHandler
	// ToolTimeout bounds each tool round-trip (--tool-timeout).
	ToolTimeout time.Duration
	// Timeout is the overall wall-clock cap (--timeout); zero = none.
	Timeout time.Duration
	// BudgetUSD is the optional spend cap (--budget-usd).
	BudgetUSD float64
	// Env adds KEY=VALUE entries to the monomind process environment.
	Env map[string]string
	// Bin overrides the monomind binary (tests); empty = discovery.
	Bin string
	// AllowBashPrefixes lets the underlying agent's own Bash tool run
	// commands starting with one of these prefixes (e.g. "monomind",
	// "monoagentcli"), on top of whatever Tools are wired — scoped, not a
	// blanket Bash grant (see agent-exec.ts's canUseTool). Real shell
	// access to a well-known CLI is far more reliable for the model to
	// actually use than a large custom tool surface alone.
	AllowBashPrefixes []string
}

// TurnResult is the terminal state of one exec turn.
type TurnResult struct {
	ExitCode   int
	SessionID  string
	ResultText string
	// StopReason is the last result event's stop_reason (protocol §3.2:
	// end_turn/max_turns/tool_round_cap/cancelled/timeout), "" if no result
	// event was ever seen. max_turns/tool_round_cap mean the turn hit a
	// limit, not that it succeeded normally — callers must not display
	// those as plain success.
	StopReason string
	// InputTokens/OutputTokens/CostUSD mirror the last usage/result event's
	// reported metrics; Has* reports whether that metric was ever actually
	// present in any event this turn (see Event.HasCostUSD etc.) — false
	// means genuinely unavailable, not zero.
	InputTokens     int64
	OutputTokens    int64
	CostUSD         float64
	HasInputTokens  bool
	HasOutputTokens bool
	HasCostUSD      bool
	// SawDone reports whether a terminal `done` event was ever observed.
	// false with Err == nil means the process/stream ended (EOF, ctx
	// cancellation notwithstanding) without ever giving terminal protocol
	// evidence — callers must treat that as interrupted, not completed.
	SawDone bool
	Err     *ProtocolError

	// incremental is the start event's streams_incrementally: assistant
	// events are deltas of one reply, not whole messages.
	incremental bool
	// streamed holds the deltas since the last tool call, and resultText
	// whether a result event supplied its own text (which then wins).
	streamed   []byte
	resultText bool
}

// ApplyEventToResult updates res's terminal-state fields (SessionID,
// ResultText, StopReason, usage, SawDone, ExitCode, Err) from one protocol
// event, applying exactly the same precedence Exec's own loop uses below.
// It deliberately does NOT handle tool_call/tool_result bridging (§4) —
// that requires writing a reply frame to the subprocess's stdin, which only
// the process actually holding that pipe can do.
//
// Exported so a caller watching the identical event stream one process
// layer removed — a GUI supervisor reading `monoagentcli chat`'s stdout,
// which is byte-for-byte this same JSON passed through from onEvent below —
// can accumulate an equivalent TurnResult without re-deriving this
// precedence logic itself (internal/ai/chatevents cannot own this instead:
// it already imports monomind for TurnResult/Event, so monomind importing
// chatevents back would cycle).
func ApplyEventToResult(res *TurnResult, ev Event) {
	switch ev.Type {
	case EventSession:
		if ev.SessionID != "" {
			res.SessionID = ev.SessionID
		}
	case EventStart:
		res.incremental = ev.StreamsIncrementally
	case EventAssistant:
		// Fallback source for ResultText: monomind's result event often has
		// no text (only assistant events do), so ResultText would otherwise
		// come back empty. An incremental runtime (start's
		// streams_incrementally) sends the reply as deltas, so they are
		// joined; otherwise each event is a whole message and the latest one
		// is the answer. A result event with its own text still wins.
		if ev.Text == "" || res.resultText {
			break
		}
		if res.incremental {
			res.streamed = append(res.streamed, ev.Text...)
			res.ResultText = string(res.streamed)
		} else {
			res.ResultText = ev.Text
		}
	case EventToolCall:
		// Text before a tool call is narration; the answer is the text
		// after the last tool round.
		res.streamed = res.streamed[:0]
	case EventUsage:
		// A snapshot, not a delta (protocol §3.2): overwrite, never
		// accumulate — the plan is explicit that summing without verified
		// delta semantics would double-count.
		applyUsage(res, ev)
	case EventResult:
		if ev.Text != "" {
			res.ResultText = ev.Text
			res.resultText = true
		}
		if ev.StopReason != "" {
			res.StopReason = ev.StopReason
		}
		applyUsage(res, ev)
		// A result explicitly marked is_error is a failure even when the
		// process later exits 0 and still sends done — e.g. the runtime
		// reported the model's answer as an error result but terminated
		// its own subprocess cleanly regardless. Only set this if nothing
		// already recorded a (fatal) error, so a fatal `error` event's own
		// code/message — richer than a bare "result reported is_error" —
		// still wins.
		if ev.IsError && res.Err == nil {
			res.Err = &ProtocolError{
				Code:    ErrRunnerError,
				Message: fmt.Sprintf("result reported is_error (stop_reason=%q)", ev.StopReason),
			}
		}
	case EventError:
		// Only a fatal error terminates the turn (protocol §3.4): a
		// non-fatal error (fatal:false) is a recoverable, mid-stream
		// hiccup — the golden fixture testdata/fixtures/bad-frame.ndjson
		// demonstrates a non-fatal error followed by a full successful
		// recovery (tool_result, assistant text, a success result, and
		// done exit_code:0), and TestFixtureExitCodesMatchContract already
		// asserts exit code 0 is correct for that fixture. Setting res.Err
		// here unconditionally would make both CLI callers (chat.go,
		// agent.go) treat that successful recovery as a hard failure and
		// discard the good ResultText. The event has already been
		// forwarded to onEvent by Exec's own loop, so callers that care
		// about non-fatal errors as they stream by still see them; only a
		// fatal error (or a later done/process exit with no success
		// signal) should surface as the turn's terminal res.Err.
		if ev.Fatal {
			res.Err = &ProtocolError{Code: ev.Code, Message: ev.ErrMessage, Fatal: ev.Fatal}
		}
	case EventDone:
		res.SawDone = true
		res.ExitCode = ev.ExitCode
		// A nonzero protocol exit_code is a failure signal on its own,
		// independent of the OS process's own exit status — the two can
		// disagree (protocol says failure, process still exits 0). Only
		// set if nothing already recorded a more specific error.
		if ev.ExitCode != 0 && res.Err == nil {
			res.Err = &ProtocolError{
				Code:     ErrRunnerError,
				Message:  fmt.Sprintf("done reported nonzero exit_code %d", ev.ExitCode),
				ExitCode: ev.ExitCode,
			}
		}
	}
}

// applyUsage overwrites res's usage snapshot from ev's metrics, field by
// field — each field only if ev actually reported it (Has*), so a usage
// event that reports tokens but omits cost does not clobber a cost figure
// captured from an earlier event in the same turn.
func applyUsage(res *TurnResult, ev Event) {
	if ev.HasInputTokens {
		res.InputTokens = ev.InputTokens
		res.HasInputTokens = true
	}
	if ev.HasOutputTokens {
		res.OutputTokens = ev.OutputTokens
		res.HasOutputTokens = true
	}
	if ev.HasCostUSD {
		res.CostUSD = ev.CostUSD
		res.HasCostUSD = true
	}
}

// toolResultFrame is the client→monomind reply for a tool_call (§4.3).
type toolResultFrame struct {
	V      int    `json:"v"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result struct {
		Text string `json:"text"`
	} `json:"result"`
}

// KillGrace bounds the window between a graceful cancel (cancel frame +
// stdin EOF) and the process-group kill on ctx cancellation. Overridable
// for tests.
var KillGrace = 5 * time.Second

// Exec runs one agent turn and invokes onEvent for every protocol event in
// arrival order. It returns the turn's terminal state: a *ProtocolError for
// error turns (also mirrored in the error event the handler received).
//
// Lifecycle contract (protocol §3.2/§3.4): the stream always ends in done;
// ctx cancellation sends a cancel frame first (best-effort graceful), then
// escalates to a process-group kill after a grace window so neither
// monomind nor an agent-CLI grandchild survives the caller.
func Exec(ctx context.Context, opts ExecOptions, onEvent func(Event)) (*TurnResult, error) {
	bin := opts.Bin
	if bin == "" {
		var err error
		bin, err = Find()
		if err != nil {
			return nil, err
		}
	}

	if opts.Prompt == "" {
		return nil, fmt.Errorf("ExecOptions.Prompt is required")
	}
	if len(opts.Tools) > 0 && opts.OnToolCall == nil {
		return nil, fmt.Errorf("ExecOptions.OnToolCall is required when Tools is set")
	}

	args := []string{"agent", "exec", "--runtime", opts.Runtime}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Cwd != "" {
		args = append(args, "--cwd", opts.Cwd)
	}
	if opts.Resume != "" {
		args = append(args, "--resume", opts.Resume)
	}
	if len(opts.AllowBashPrefixes) > 0 {
		args = append(args, "--allow-bash-prefix", strings.Join(opts.AllowBashPrefixes, ","))
	}
	if opts.Timeout > 0 {
		args = append(args, "--timeout", formatDuration(opts.Timeout))
	}
	if opts.BudgetUSD > 0 {
		args = append(args, "--budget-usd", fmt.Sprintf("%g", opts.BudgetUSD))
	}
	if opts.ToolTimeout > 0 {
		args = append(args, "--tool-timeout", formatDuration(opts.ToolTimeout))
	}

	cleanup := []func(){}
	defer func() {
		for _, f := range cleanup {
			f()
		}
	}()

	// The prompt always travels via --prompt-file (written to a temp file),
	// mirroring --system-file: large prompts (e.g. agentgen's HTML payload)
	// must never hit argv limits.
	promptF, err := os.CreateTemp("", "monoagent-prompt-*.md")
	if err != nil {
		return nil, fmt.Errorf("write prompt file: %w", err)
	}
	if _, err := promptF.WriteString(opts.Prompt); err != nil {
		promptF.Close()
		os.Remove(promptF.Name())
		return nil, fmt.Errorf("write prompt file: %w", err)
	}
	promptF.Close()
	promptName := promptF.Name()
	cleanup = append(cleanup, func() { os.Remove(promptName) })
	args = append(args, "--prompt-file", promptName)

	if opts.SystemPrompt != "" {
		f, err := os.CreateTemp("", "monoagent-system-*.md")
		if err != nil {
			return nil, fmt.Errorf("write system prompt: %w", err)
		}
		if _, err := f.WriteString(opts.SystemPrompt); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, fmt.Errorf("write system prompt: %w", err)
		}
		f.Close()
		name := f.Name()
		cleanup = append(cleanup, func() { os.Remove(name) })
		args = append(args, "--system-file", name)
	}

	if len(opts.Tools) > 0 {
		b, err := json.Marshal(opts.Tools)
		if err != nil {
			return nil, fmt.Errorf("marshal tools: %w", err)
		}
		f, err := os.CreateTemp("", "monoagent-tools-*.json")
		if err != nil {
			return nil, fmt.Errorf("write tools file: %w", err)
		}
		if _, err := f.Write(b); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, fmt.Errorf("write tools file: %w", err)
		}
		f.Close()
		name := f.Name()
		cleanup = append(cleanup, func() { os.Remove(name) })
		args = append(args, "--tools", "stdio", "--tools-file", name)
	} else {
		args = append(args, "--tools", "none")
	}

	cmd := exec.Command(bin, args...)
	// Always build a filtered environment (not only when opts.Env is set):
	// when a Claude Code session (this app itself, or anything upstream)
	// runs monoagentcli, its own CLAUDECODE/CLAUDE_CODE_*/CLAUDE_PID session
	// markers are ambient in the process environment — set globally for the
	// whole login session (confirmed directly: still present even in a
	// process launched via `open`, with PPID=1, completely detached from
	// any parent shell). The `claude` CLI reads these as "I'm already
	// running inside a Claude Code session" and changes its own auth
	// resolution accordingly, which surfaces as "Not logged in" for a
	// separate, independently-launched `claude` login — even though the
	// user's own Keychain-stored credentials are perfectly valid. Stripping
	// them here means every chat/agent turn gets a clean environment
	// regardless of what launched monoagentcli.
	cmd.Env = append(FilteredEnviron(), envSlice(opts.Env)...)
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr // monomind keeps diagnostics off stdout (§3)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start monomind: %w", err)
	}

	res := &TurnResult{}
	events := make(chan Event, 64)
	var stdinMu sync.Mutex
	var stdinOnce sync.Once

	writeLine := func(b []byte) {
		stdinMu.Lock()
		defer stdinMu.Unlock()
		_, _ = stdin.Write(append(b, '\n'))
	}
	closeStdin := func() {
		stdinOnce.Do(func() {
			stdinMu.Lock()
			defer stdinMu.Unlock()
			_ = stdin.Close()
		})
	}

	// stdout reader: one JSON object per line (§3); closes events on exit.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(events)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 || line[0] != '{' {
				continue // tolerate stray non-JSON output
			}
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line) // sc.Bytes() is reused across scans
			var ev Event
			if err := json.Unmarshal(lineCopy, &ev); err != nil {
				continue // malformed line: skip, keep streaming
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Event loop: fan out to the caller, bridge tool calls, capture state.
	var toolWg sync.WaitGroup
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		for ev := range events {
			if onEvent != nil {
				onEvent(ev)
			}
			ApplyEventToResult(res, ev)
			switch ev.Type {
			case EventToolCall:
				if opts.OnToolCall == nil {
					// Protocol mismatch: the subprocess emitted a tool_call
					// even though this turn never enabled tools (opts.Tools
					// is empty, so opts.OnToolCall is nil too — the common
					// no-tools case). Calling a nil handler here would
					// panic in this background goroutine and crash the
					// entire hosting process (CLI or Wails GUI backend),
					// not just this turn. Report it as a protocol error
					// instead, and reply with an ok:false tool_result frame
					// so a real subprocess waiting on the round-trip can
					// react instead of hanging.
					res.Err = &ProtocolError{
						Code:    ErrBadFrame,
						Message: fmt.Sprintf("received tool_call %q for %q with no OnToolCall handler configured (Tools not set on this turn)", ev.ID, ev.Name),
						Fatal:   true,
					}
					frame := toolResultFrame{V: ProtocolVersion, Type: "tool_result", ID: ev.ID}
					frame.OK = false
					frame.Result.Text = "no tool handler configured for this turn"
					if b, err := json.Marshal(frame); err == nil {
						writeLine(b)
					}
					continue
				}
				toolWg.Add(1)
				go func(ev Event) {
					defer toolWg.Done()
					text, err := opts.OnToolCall(ctx, ev.Name, ev.Args)
					frame := toolResultFrame{V: ProtocolVersion, Type: "tool_result", ID: ev.ID}
					if err != nil {
						frame.OK = false
						frame.Result.Text = err.Error()
					} else {
						frame.OK = true
						frame.Result.Text = text
					}
					if b, err := json.Marshal(frame); err == nil {
						writeLine(b)
					}
				}(ev)
			}
		}
	}()

	// cmd.Wait must not run concurrently with the reader goroutine: per the
	// os/exec docs, "Wait will close the pipe after seeing the command
	// exit... it is thus incorrect to call Wait before all reads from the
	// pipe have completed." Wait() closes stdout's read end as part of its
	// cleanup once the child is reaped; if that races with the scanner's
	// in-flight Read, the read is torn down mid-call instead of draining
	// remaining buffered bytes and returning a clean EOF — silently
	// dropping already-written lines (observed as res.Err/res.ExitCode
	// staying zero-valued for a process that actually emitted them).
	// Waiting for readerDone first serializes "drain stdout" before
	// "reap+close", eliminating the race without risking a deadlock: the
	// reader always terminates (natural EOF, scan error, or ctx.Done), so
	// this can't block forever waiting to call Wait.
	waitCh := make(chan error, 1)
	go func() {
		<-readerDone
		waitCh <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Graceful first (cancel frame + EOF), bounded by a group kill.
		writeLine([]byte(`{"v":1,"type":"cancel"}`))
		closeStdin()
		killTimer := time.AfterFunc(KillGrace, func() { killProcessGroup(cmd, cmd.Process.Pid) })
		defer killTimer.Stop()
		<-loopDone
		<-waitCh
		toolWg.Wait()
		if res.ExitCode == 0 && res.Err == nil {
			res.Err = &ProtocolError{Code: ErrCancelled, Message: "cancelled by caller", ExitCode: 130}
			res.ExitCode = 130
		}
		return res, nil

	case err := <-waitCh:
		// Process exited — drain buffered events, then finish up.
		<-readerDone
		<-loopDone
		toolWg.Wait()
		closeStdin()
		if res.ExitCode == 0 && res.Err == nil && err != nil {
			res.Err = &ProtocolError{Code: ErrRunnerError, Message: fmt.Sprintf("monomind exited: %v", err), ExitCode: 1}
			res.ExitCode = 1
		}
		return res, nil
	}
}

// sessionMarkerEnvVars are Claude Code's own session-identity env vars —
// see the comment at Exec's cmd.Env construction for why these must never
// reach a spawned `claude` CLI invocation.
var sessionMarkerEnvVars = []string{
	"CLAUDECODE",
	"CLAUDE_PID",
	"CLAUDE_EFFORT",
}

// sessionMarkerEnvPrefixes catches every CLAUDE_CODE_* variant without
// needing to enumerate them (new ones can be added by Claude Code itself
// without this list going stale), and every MONOMIND_* scoping override
// (notably MONOMIND_CWD): those are per-call configuration, set
// explicitly via opts.Env / individual command builders — an ambient value
// inherited from whatever launched this process must not reach the child,
// where a duplicate entry could shadow the intended per-profile scoping.
var sessionMarkerEnvPrefixes = []string{
	"CLAUDE_CODE_",
	"MONOMIND_",
}

// FilteredEnviron returns the current process environment with Claude
// Code's session-marker variables and ambient MONOMIND_* overrides
// removed. Exported so command builders outside this package (e.g. the
// chat KG tools) build children with the identical stripped base before
// appending their own explicit MONOMIND_* values.
func FilteredEnviron() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if slices.Contains(sessionMarkerEnvVars, key) {
			continue
		}
		skip := false
		for _, prefix := range sessionMarkerEnvPrefixes {
			if strings.HasPrefix(key, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// envSlice flattens a map into KEY=VALUE strings.
func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// formatDuration renders a duration in the protocol's duration syntax
// (sub-second is ms-suffixed; everything else whole seconds).
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%ds", int((d+time.Second/2)/time.Second))
}

// Scan proxies `monomind agent scan --json` (protocol §6). Scanning always
// exits 0 on the monomind side; errors here mean monomind is missing/broken.
//
// Handshakes via Ensure() first: an installed-but-too-old monomind answers
// an unrecognized `agent scan --json` invocation with human help text
// instead of JSON (exit 0), which would otherwise surface as a confusing
// "unparseable output" JSON error instead of the actionable "monomind X is
// too old" message every other entry point already gives.
func Scan(ctx context.Context) (*ScanResult, error) {
	bin, _, err := Ensure(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "agent", "scan", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("agent scan failed: %w", err)
	}
	var res ScanResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("agent scan: unparseable output: %w", err)
	}
	return &res, nil
}
