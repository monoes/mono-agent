package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// countingExecutor fails with errs[i] on call i (the last error repeats);
// a nil entry succeeds.
type countingExecutor struct {
	typ   string
	calls int
	errs  []error
}

func (c *countingExecutor) Type() string { return c.typ }

func (c *countingExecutor) Execute(context.Context, NodeInput, map[string]interface{}) ([]NodeOutput, error) {
	i := c.calls
	c.calls++
	if i >= len(c.errs) {
		i = len(c.errs) - 1
	}
	if c.errs[i] != nil {
		return nil, c.errs[i]
	}
	return []NodeOutput{{Handle: "main"}}, nil
}

// noWait retries three times without sleeping (InitialDelay 0).
var noWait = RetryPolicy{MaxRetries: 3, BackoffType: "fixed"}

func TestExecuteWithRetry_DeterministicErrorsNotRetried(t *testing.T) {
	cases := map[string]error{
		"paused":            fmt.Errorf("hil: %w", ErrNodePaused),
		"invalid config":    fmt.Errorf("node x: %w", ErrInvalidConfig),
		"permanent":         Permanent(errors.New("404 not found")),
		"wrapped permanent": fmt.Errorf("outer: %w", Permanent(errors.New("bad"))),
		"canceled":          fmt.Errorf("inner: %w", context.Canceled),
		"deadline":          fmt.Errorf("inner: %w", context.DeadlineExceeded),
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			ex := &countingExecutor{typ: "t", errs: []error{e}}
			_, err := executeWithRetry(context.Background(), ex, NodeInput{}, nil, noWait)
			if ex.calls != 1 {
				t.Fatalf("executed %d times, want 1", ex.calls)
			}
			if err != e {
				t.Fatalf("err = %v, want %v", err, e)
			}
		})
	}
}

func TestExecuteWithRetry_TransientRetriedPerPolicy(t *testing.T) {
	ex := &countingExecutor{errs: []error{errors.New("connection reset")}}
	_, err := executeWithRetry(context.Background(), ex, NodeInput{}, nil, noWait)
	if err == nil || ex.calls != 4 {
		t.Fatalf("calls=%d err=%v, want 4 calls and an error", ex.calls, err)
	}

	// Succeeds on the third attempt.
	ex = &countingExecutor{errs: []error{errors.New("a"), errors.New("b"), nil}}
	if _, err := executeWithRetry(context.Background(), ex, NodeInput{}, nil, noWait); err != nil || ex.calls != 3 {
		t.Fatalf("calls=%d err=%v", ex.calls, err)
	}
}

func TestPermanentError(t *testing.T) {
	if Permanent(nil) != nil {
		t.Fatal("Permanent(nil) must be nil")
	}
	base := errors.New("gone")
	p := Permanent(base)
	var pe *PermanentError
	if !errors.As(p, &pe) || !errors.Is(p, base) || p.Error() != "gone" {
		t.Fatalf("PermanentError wrapping broken: %v", p)
	}
}
