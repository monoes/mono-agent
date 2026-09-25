package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// withRetrySeams replaces the sleep seam (recording delays) and the
// classifier for one test.
func withRetrySeams(t *testing.T, cls func(context.Context, string, error, int) (bool, time.Duration)) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	oldSleep, oldCls := retrySleep, RetryClassifier
	retrySleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}
	RetryClassifier = cls
	t.Cleanup(func() { retrySleep, RetryClassifier = oldSleep, oldCls })
	return &slept
}

var threeRetries = RetryPolicy{MaxRetries: 3, BackoffType: "fixed", InitialDelay: 2}

func TestExecuteWithRetry_ClassifierPermanentStops(t *testing.T) {
	var gotType string
	var gotAttempt int
	withRetrySeams(t, func(_ context.Context, nodeType string, _ error, attempt int) (bool, time.Duration) {
		gotType, gotAttempt = nodeType, attempt
		return false, 0
	})
	ex := &countingExecutor{typ: "http.request", errs: []error{errors.New("401 unauthorized")}}
	_, err := executeWithRetry(context.Background(), ex, NodeInput{}, nil, threeRetries)
	if err == nil || ex.calls != 1 {
		t.Fatalf("calls=%d err=%v", ex.calls, err)
	}
	if gotType != "http.request" || gotAttempt != 1 {
		t.Fatalf("classifier got type=%q attempt=%d", gotType, gotAttempt)
	}
}

func TestExecuteWithRetry_ClassifierPanicRetriesAsToday(t *testing.T) {
	slept := withRetrySeams(t, func(context.Context, string, error, int) (bool, time.Duration) {
		panic("boom")
	})
	ex := &countingExecutor{errs: []error{errors.New("x")}}
	if _, err := executeWithRetry(context.Background(), ex, NodeInput{}, nil, threeRetries); err == nil || ex.calls != 4 {
		t.Fatalf("calls=%d err=%v", ex.calls, err)
	}
	if len(*slept) != 3 || (*slept)[0] != 2*time.Second {
		t.Fatalf("slept %v", *slept)
	}
}

func TestExecuteWithRetry_ClassifierDelayOverride(t *testing.T) {
	slept := withRetrySeams(t, func(_ context.Context, _ string, _ error, attempt int) (bool, time.Duration) {
		if attempt == 1 {
			return true, 30 * time.Second
		}
		if attempt == 2 {
			return true, time.Hour // capped
		}
		return true, 0 // backoff as policy
	})
	ex := &countingExecutor{errs: []error{errors.New("429")}}
	_, _ = executeWithRetry(context.Background(), ex, NodeInput{}, nil, threeRetries)
	want := []time.Duration{30 * time.Second, maxClassifierDelay, 2 * time.Second}
	if fmt.Sprint(*slept) != fmt.Sprint(want) {
		t.Fatalf("slept %v, want %v", *slept, want)
	}
}

func TestExecuteWithRetry_NoPolicyNeverClassifies(t *testing.T) {
	called := 0
	withRetrySeams(t, func(context.Context, string, error, int) (bool, time.Duration) {
		called++
		return true, 0
	})
	ex := &countingExecutor{errs: []error{errors.New("x")}}
	_, _ = executeWithRetry(context.Background(), ex, NodeInput{}, nil, RetryPolicy{})
	if ex.calls != 1 || called != 0 {
		t.Fatalf("calls=%d classified=%d", ex.calls, called)
	}
}

func TestExecuteWithRetry_ClassifierSeesConfigSecretsScrubbed(t *testing.T) {
	var msg string
	var inner error
	withRetrySeams(t, func(_ context.Context, _ string, err error, _ int) (bool, time.Duration) {
		msg, inner = err.Error(), err
		return false, 0
	})
	orig := errors.New("login failed for hunter2-long-password at host")
	ex := &countingExecutor{errs: []error{orig}}
	cfg := map[string]interface{}{"password": "hunter2-long-password", "nested": map[string]interface{}{"api_key": "abc"}}
	_, _ = executeWithRetry(context.Background(), ex, NodeInput{}, cfg, threeRetries)
	if strings.Contains(msg, "hunter2") || !strings.Contains(msg, "login failed for ***") {
		t.Fatalf("classifier saw %q", msg)
	}
	if !errors.Is(inner, orig) {
		t.Fatal("scrubbed error must still unwrap to the original")
	}
}

func TestExecuteWithRetry_TypedErrorsNeverClassified(t *testing.T) {
	called := 0
	withRetrySeams(t, func(context.Context, string, error, int) (bool, time.Duration) {
		called++
		return true, 0
	})
	for _, e := range []error{ErrNodePaused, ErrInvalidConfig, Permanent(errors.New("x")), context.Canceled} {
		ex := &countingExecutor{errs: []error{e}}
		_, _ = executeWithRetry(context.Background(), ex, NodeInput{}, nil, threeRetries)
	}
	if called != 0 {
		t.Fatalf("classifier called %d times for typed errors", called)
	}
}

func TestExecuteWithRetry_NilClassifierUsesPolicyBackoff(t *testing.T) {
	slept := withRetrySeams(t, nil)
	ex := &countingExecutor{errs: []error{errors.New("x")}}
	_, _ = executeWithRetry(context.Background(), ex, NodeInput{}, nil,
		RetryPolicy{MaxRetries: 3, BackoffType: "exponential", InitialDelay: 1})
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if ex.calls != 4 || fmt.Sprint(*slept) != fmt.Sprint(want) {
		t.Fatalf("calls=%d slept=%v", ex.calls, *slept)
	}
}

func TestExecuteWithRetry_CancelDuringWait(t *testing.T) {
	withRetrySeams(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	retrySleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
	ex := &countingExecutor{errs: []error{errors.New("x")}}
	if _, err := executeWithRetry(ctx, ex, NodeInput{}, nil, threeRetries); !errors.Is(err, ErrExecutionCancelled) || ex.calls != 1 {
		t.Fatalf("calls=%d err=%v", ex.calls, err)
	}
}
