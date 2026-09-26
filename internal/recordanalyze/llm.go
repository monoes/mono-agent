package recordanalyze

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/monomind"
)

// Runner asks the AI for one completion. The production runner is
// ExecRunner (monomind agent exec); tests use a stub.
type Runner interface {
	Run(ctx context.Context, prompt string) (string, error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(ctx context.Context, prompt string) (string, error)

func (f RunnerFunc) Run(ctx context.Context, prompt string) (string, error) { return f(ctx, prompt) }

// Defaults for ExecRunner.
const (
	DefaultRuntime   = "claude"
	DefaultTimeout   = 10 * time.Minute
	DefaultBudgetUSD = 2.0
)

// ExecRunner runs the prompt as one `monomind agent exec` turn, like
// capturesummary.ExecRunner: no tools, no session, no chat history. It is
// the only AI backend of this package; the in-app AI provider is never used.
type ExecRunner struct {
	Runtime   string // default DefaultRuntime
	Model     string // "" leaves it to the runtime
	BudgetUSD float64
	Timeout   time.Duration
}

func (r ExecRunner) Run(ctx context.Context, prompt string) (string, error) {
	tail := &tailWriter{max: stderrTailBytes}
	text, err := r.run(ctx, prompt, io.MultiWriter(os.Stderr, tail))
	if err != nil {
		return "", withStderrTail(withRunnerHint(err, r.runtime()), tail.String())
	}
	return text, nil
}

// stderrTailBytes is how much of the runner's stderr a failure carries.
const stderrTailBytes = 2048

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if over := len(w.buf) - w.max; over > 0 {
		w.buf = append([]byte(nil), w.buf[over:]...)
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

var (
	bearerRe     = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	credAssignRe = regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret|password|passwd|authorization|x-api-key|cookie)["']?\s*[:=]\s*["']?)[^\s"',;]+`)
)

// redactDiagnostics strips credentials from runner output before it is
// shown: known token shapes, bearer headers, key=value credentials and
// sensitive URL parameters.
func redactDiagnostics(s string) string {
	s = secretTokenRe.ReplaceAllString(s, Redacted)
	s = bearerRe.ReplaceAllString(s, "${1}"+Redacted)
	s = credAssignRe.ReplaceAllString(s, "${1}"+Redacted)
	return sanitizeText(s)
}

// withStderrTail appends the redacted stderr tail to a runner error.
func withStderrTail(err error, tail string) error {
	tail = strings.TrimSpace(redactDiagnostics(tail))
	if tail == "" {
		return err
	}
	return fmt.Errorf("%w\nrunner stderr (last %d bytes):\n%s", err, stderrTailBytes, tail)
}

func (r ExecRunner) runtime() string {
	if r.Runtime == "" {
		return DefaultRuntime
	}
	return r.Runtime
}

// withRunnerHint adds what to try next to a runner failure (e2e D12).
func withRunnerHint(err error, runtime string) error {
	msg := strings.ToLower(err.Error())
	hint := fmt.Sprintf("check the %s runtime with `monomind doctor`", runtime)
	switch {
	case strings.Contains(msg, "not found") && strings.Contains(msg, "monomind"),
		strings.Contains(msg, "executable file not found"):
		hint = "monomind is not installed or not on PATH; install it (npx -y monomind@latest doctor)"
	case strings.Contains(msg, "auth"), strings.Contains(msg, "credential"), strings.Contains(msg, "api key"),
		strings.Contains(msg, "api_key"), strings.Contains(msg, "401"), strings.Contains(msg, "login"), strings.Contains(msg, "unauthorized"):
		hint = fmt.Sprintf("no %s credentials? run `monomind doctor`", runtime)
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		hint = "the AI turn timed out; retry, or raise --timeout"
	case strings.Contains(msg, "budget"):
		hint = "the turn hit its spend cap"
	}
	return fmt.Errorf("%w (hint: %s)", err, hint)
}

func (r ExecRunner) run(ctx context.Context, prompt string, stderr io.Writer) (string, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bin, _, err := monomind.Ensure(ctx)
	if err != nil {
		return "", err
	}
	runtime := r.Runtime
	if runtime == "" {
		runtime = DefaultRuntime
	}
	budget := r.BudgetUSD
	if budget <= 0 {
		budget = DefaultBudgetUSD
	}
	opts := monomind.ExecOptions{Bin: bin, Runtime: runtime, Model: r.Model, Prompt: prompt, BudgetUSD: budget, Stderr: stderr}
	if deadline, ok := ctx.Deadline(); ok {
		// Let the runtime stop itself a little before we would kill it.
		if d := time.Until(deadline) - 5*time.Second; d > 0 {
			opts.Timeout = d
		}
	}
	res, err := monomind.Exec(ctx, opts, nil)
	if err != nil {
		return "", err
	}
	if res.Err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(res.Err.Code+" "+res.Err.Message))
	}
	switch res.StopReason {
	case "", "end_turn":
	default:
		return "", fmt.Errorf("the turn stopped early (%s)", res.StopReason)
	}
	if !res.SawDone && res.ExitCode != 0 {
		return "", fmt.Errorf("the runtime exited with code %d", res.ExitCode)
	}
	return res.ResultText, nil
}

// DraftAutomation is the automation block of the AI output.
type DraftAutomation struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Site        *automation.Site  `json:"site,omitempty"`
	Login       *automation.Login `json:"login,omitempty"`
}

// Output is the strict JSON the AI returns (data/skills/record-to-action.md).
type Output struct {
	Automation DraftAutomation                 `json:"automation"`
	Action     action.ActionDef                `json:"action"`
	Selectors  map[string]action.SelectorEntry `json:"selectors"`
	Fragments  []action.FragmentDef            `json:"fragments,omitempty"`
	Scripts    map[string]string               `json:"scripts,omitempty"`
	Names      map[string]string               `json:"names,omitempty"`
}

// Generate asks the runner for a draft, validates it, and on failure runs
// one repair round fed with the validator errors. A second failure returns
// an error carrying the validator output.
func Generate(ctx context.Context, r Runner, prompt string, env *Env) (*Output, error) {
	text, err := r.Run(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("AI draft: %w", err)
	}
	out, problems := parseAndCheck(text, env)
	if len(problems) == 0 {
		return out, nil
	}
	text, err = r.Run(ctx, RepairPrompt(prompt, text, problems))
	if err != nil {
		return nil, fmt.Errorf("AI repair: %w", err)
	}
	out, problems = parseAndCheck(text, env)
	if len(problems) > 0 {
		return nil, fmt.Errorf("AI draft failed validation after one repair round:\n- %s", strings.Join(problems, "\n- "))
	}
	return out, nil
}

func parseAndCheck(text string, env *Env) (*Output, []string) {
	raw := extractJSON(text)
	if raw == "" {
		return nil, []string{"the answer contains no JSON object"}
	}
	var out Output
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&out); err != nil {
		return nil, []string{"invalid JSON: " + err.Error()}
	}
	FixNames(&out, env)
	return &out, CheckOutput(&out, env)
}

// extractJSON returns the JSON object in text: a ```json fence's body, or
// the span from the first '{' to the last '}'.
func extractJSON(text string) string {
	t := strings.TrimSpace(text)
	if i := strings.Index(t, "```"); i >= 0 {
		body := t[i+3:]
		body = strings.TrimPrefix(body, "json")
		if j := strings.Index(body, "```"); j >= 0 {
			body = body[:j]
		}
		if b := strings.TrimSpace(body); strings.HasPrefix(b, "{") {
			return b
		}
	}
	a, b := strings.IndexByte(t, '{'), strings.LastIndexByte(t, '}')
	if a < 0 || b <= a {
		return ""
	}
	return t[a : b+1]
}
