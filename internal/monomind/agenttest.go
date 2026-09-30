package monomind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CapAgentTestJSON is `monomind agent test <id> [--model M] --json`
// (monomind#390, protocol rev 18 §13): one structured result per
// runtime and model.
const CapAgentTestJSON = "agent-test-json"

// AgentTestResult is `agent test --json`'s output (protocol §13). Nullable
// fields are pointers: nil means monomind reported null (unknown), not 0.
type AgentTestResult struct {
	V              int      `json:"v"`
	Runtime        string   `json:"runtime"`
	Model          *string  `json:"model"`
	Status         string   `json:"status"`
	Reply          *string  `json:"reply"`
	LatencyFirstMs *int64   `json:"latency_first_ms"`
	LatencyMs      int64    `json:"latency_ms"`
	InputTokens    *int64   `json:"input_tokens"`
	OutputTokens   *int64   `json:"output_tokens"`
	CostUSD        *float64 `json:"cost_usd"`
	CostEstimated  bool     `json:"cost_estimated"`
	RuntimeVersion *string  `json:"runtime_version"`
	Error          *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		LoginHint string `json:"login_hint,omitempty"`
	} `json:"error"`
}

// ErrAgentTestUnsupported wraps AgentTest's error when monomind exited
// fast without a result: the command itself failed (not this monomind's,
// or it rejected the arguments) before any model call, so a caller can
// test another way without paying for a second call.
var ErrAgentTestUnsupported = errors.New("agent test gave no result")

// agentTestFastFail is how quickly a run without JSON must end to count as
// ErrAgentTestUnsupported rather than a failed test.
var agentTestFastFail = 5 * time.Second

// agentTestGrace is how long past its own --timeout monomind gets to report
// before the Go-side deadline kills it.
var agentTestGrace = 15 * time.Second

// AgentTest runs `monomind agent test <runtime> --json [--model=M]
// --timeout T`. A failed status still returns a result (monomind exits 1 or
// 124 with the JSON on stdout); an error means no result could be read,
// wrapping context.DeadlineExceeded when monomind overran timeout plus a
// grace period, and ErrAgentTestUnsupported when it failed fast. model ""
// tests the runtime's default. Cancelling ctx kills monomind's whole
// process group, including the runtime it started.
func AgentTest(ctx context.Context, bin, runtime, model string, timeout time.Duration) (*AgentTestResult, error) {
	// runtime is positional and model a flag value: a leading dash would be
	// read as a flag of its own.
	if runtime == "" || strings.HasPrefix(runtime, "-") {
		return nil, fmt.Errorf("invalid runtime %q", runtime)
	}
	if strings.HasPrefix(model, "-") {
		return nil, fmt.Errorf("invalid model %q: must not start with \"-\"", model)
	}
	if bin == "" {
		var err error
		if bin, err = Find(); err != nil {
			return nil, err
		}
	}
	args := []string{"agent", "test", runtime, "--json"}
	if model != "" {
		args = append(args, "--model="+model)
	}
	parent := ctx
	if timeout > 0 {
		args = append(args, "--timeout", formatDuration(timeout))
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout+agentTestGrace)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = PinEnv(FilteredEnviron(), bin)
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd, 0)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	release, err := startProcessGroup(cmd)
	if err != nil {
		return nil, fmt.Errorf("start monomind agent test: %w", err)
	}
	defer release()
	runErr := cmd.Wait()

	var res AgentTestResult
	out := bytes.TrimSpace(stdout.Bytes())
	if err := json.Unmarshal(lastJSONDocument(out), &res); err != nil || res.Status == "" {
		if runErr == nil {
			runErr = fmt.Errorf("unparseable output %q", clipOutput(string(out)))
		}
		switch {
		case parent.Err() != nil:
			runErr = fmt.Errorf("%w: %w", parent.Err(), runErr)
		case ctx.Err() != nil:
			runErr = fmt.Errorf("no result within %s: %w", formatDuration(timeout+agentTestGrace), context.DeadlineExceeded)
		case time.Since(start) < agentTestFastFail:
			runErr = fmt.Errorf("%w: %w", ErrAgentTestUnsupported, runErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("monomind agent test %s: %w: %s", runtime, runErr, clipOutput(msg))
		}
		return nil, fmt.Errorf("monomind agent test %s: %w", runtime, runErr)
	}
	return &res, nil
}

func clipOutput(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
