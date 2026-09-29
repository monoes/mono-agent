package monomind

import (
	"bytes"
	"context"
	"encoding/json"
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

// AgentTest runs `monomind agent test <runtime> [--model M] --timeout T
// --json`. A failed status still returns a result (monomind exits 1 or 124
// with the JSON on stdout); an error means no result could be read. model
// "" tests the runtime's default. Cancelling ctx kills monomind's whole
// process group, including the runtime it started.
func AgentTest(ctx context.Context, bin, runtime, model string, timeout time.Duration) (*AgentTestResult, error) {
	if bin == "" {
		var err error
		if bin, err = Find(); err != nil {
			return nil, err
		}
	}
	args := []string{"agent", "test", runtime, "--json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if timeout > 0 {
		args = append(args, "--timeout", fmt.Sprintf("%ds", int(timeout.Round(time.Second).Seconds())))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = FilteredEnviron()
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd, 0)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
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
