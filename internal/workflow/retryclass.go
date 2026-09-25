package workflow

import (
	"context"
	"sort"
	"strings"
	"time"
)

// RetryClassifier, when set, is asked before each retry of an opaque node
// error — one that passed the deterministic filter (isNonRetryable) and would
// be retried under the node's retry_policy. It returns retry=false to stop
// retrying, and a delay > 0 to replace the policy's backoff for that retry
// (capped at maxClassifierDelay). attempt is the 1-based number of the
// attempt that just failed.
//
// nil (the default) keeps today's behaviour. A panic inside the classifier is
// recovered and treated as "retry as today". The workflow package never
// imports a classifier implementation: the CLI installs one at start-up
// (cmd/monoagentcli/retry_jev.go), so the engine stays dependency-free.
//
// The error handed to the classifier has the values of sensitive config keys
// (password, token, api_key, …; see redactKeyPattern) replaced by "***" in
// its message; it still unwraps to the original error.
var RetryClassifier func(ctx context.Context, nodeType string, err error, attempt int) (retry bool, delay time.Duration)

// maxClassifierDelay caps a classifier-provided delay.
const maxClassifierDelay = 5 * time.Minute

// retrySleep waits d or until ctx is done (returning ctx.Err()). A variable so
// tests can observe delays without sleeping.
var retrySleep = func(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// classifyRetry consults RetryClassifier (panic-safe). Without a classifier,
// or on panic, it answers "retry with the policy's backoff".
func classifyRetry(ctx context.Context, nodeType string, err error, attempt int, config map[string]interface{}) (retry bool, delay time.Duration) {
	cls := RetryClassifier
	if cls == nil {
		return true, 0
	}
	defer func() {
		if r := recover(); r != nil {
			retry, delay = true, 0
		}
	}()
	retry, delay = cls(ctx, nodeType, scrubConfigSecrets(err, config), attempt)
	if delay < 0 {
		delay = 0
	}
	if delay > maxClassifierDelay {
		delay = maxClassifierDelay
	}
	return retry, delay
}

// scrubbedError carries a redacted message while unwrapping to the original.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// scrubConfigSecrets returns err with every value found under a sensitive
// config key (at any depth) replaced by RedactedValue in its message. The
// node config is already resolved here, so these are the plaintext
// credentials the node was given.
func scrubConfigSecrets(err error, config map[string]interface{}) error {
	var secrets []string
	collectSecretValues(config, false, 0, &secrets)
	if len(secrets) == 0 {
		return err
	}
	// Longest first so a secret containing another is replaced whole.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	msg := err.Error()
	for _, s := range secrets {
		msg = strings.ReplaceAll(msg, s, RedactedValue)
	}
	if msg == err.Error() {
		return err
	}
	return &scrubbedError{msg: msg, err: err}
}

func collectSecretValues(v any, sensitive bool, depth int, out *[]string) {
	if depth > MaxRedactDepth {
		return
	}
	switch x := v.(type) {
	case map[string]interface{}:
		for k, vv := range x {
			collectSecretValues(vv, sensitive || redactKeyPattern.MatchString(k), depth+1, out)
		}
	case []interface{}:
		for _, vv := range x {
			collectSecretValues(vv, sensitive, depth+1, out)
		}
	case string:
		// Very short values would shred unrelated text; they carry little
		// secret material anyway.
		if sensitive && len(x) >= 3 {
			*out = append(*out, x)
		}
	}
}
