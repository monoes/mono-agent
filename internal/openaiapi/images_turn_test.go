package openaiapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// A runtime's own failures are classified as they are for chat.
func TestImagesRuntimeErrors(t *testing.T) {
	for name, c := range map[string]struct {
		exec   execFunc
		status int
		code   string
	}{
		"a quota":          {scriptedExec(evStart(false, "workspace-write"), evError(monomind.ErrQuota, "usage limit reached"), evDone(1)), 429, "insufficient_quota"},
		"a rate limit":     {scriptedExec(evStart(false, "workspace-write"), evError(monomind.ErrRateLimited, "slow down"), evDone(1)), 429, "rate_limit_exceeded"},
		"a timeout":        {scriptedExec(evStart(false, "workspace-write"), evError(monomind.ErrTimeout, "too slow"), evDone(1)), 504, "timeout"},
		"not signed in":    {scriptedExec(evStart(false, "workspace-write"), evError(monomind.ErrAuth, "please sign in"), evDone(1)), 503, "runtime_not_available"},
		"a runner failure": {scriptedExec(evStart(false, "workspace-write"), evError("runner_crashed", "boom"), evDone(1)), 502, "runtime_error"},
		"no done":          {scriptedExec(evStart(false, "workspace-write"), evText("x")), 502, "runtime_error"},
		"a sandbox that cannot be applied": {func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
			return nil, monomind.ErrSandboxRequired
		}, 403, "policy_denied"},
	} {
		// A runtime that failed may still have left an image: only a clean turn is collected.
		failing := func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
			_ = os.WriteFile(filepath.Join(o.Cwd, "a.png"), pngBytes, 0o600)
			return c.exec(ctx, o, onEvent)
		}
		h := newHarness(t, failing)
		rec := postImages(h, anyPolicy, h.key(t, "default", "app", false), `{"model":"codex","prompt":"x"}`)
		if rec.Code != c.status || decodeErrorBody(t, rec)["code"] != c.code {
			t.Errorf("%s: %d %s, want %d %s", name, rec.Code, rec.Body, c.status, c.code)
		}
		if rec.Header().Get("X-Monoagent-Model") != "codex/default" {
			t.Errorf("%s: X-Monoagent-Model = %q", name, rec.Header().Get("X-Monoagent-Model"))
		}
	}
}

// The class is checked again when the turn starts, against what the runtime reports:
// a turn that is weaker than the policy allows is cancelled, and its image is not served.
func TestImagesTurnThatStartsWeakerThanThePolicyIsRefused(t *testing.T) {
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		_ = os.WriteFile(filepath.Join(o.Cwd, "a.png"), pngBytes, 0o600)
		return scriptedExec(evStart(false, "none"), evText("a.png"), evResult("a.png", monomind.StopEndTurn), evDone(0))(ctx, o, onEvent)
	})
	rec := postImages(h, Policy{Max: Sandboxed}, h.key(t, "default", "app", false), `{"model":"codex","prompt":"x"}`)
	if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// An image request takes a slot like a chat one: a full server answers 429 and starts nothing.
func TestImagesShareTheConcurrencyLimitWithChat(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	var spawned atomic.Int32
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		if spawned.Add(1) == 1 {
			started <- struct{}{}
			<-release
		}
		return imageTurn("a.png", onePNG("a.png"))(ctx, o, onEvent)
	}, func(_ *Deps, c *Config) { c.MaxConcurrent = 1 })
	secret := h.key(t, "default", "app", false)

	done := make(chan int, 1)
	go func() {
		done <- post(h, anyPolicy, secret, `{"model":"claude","messages":[{"role":"user","content":"hold the slot"}]}`).Code
	}()
	waitStarted(t, started)
	rec := postImages(h, anyPolicy, secret, `{"prompt":"x"}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" || decodeErrorBody(t, rec)["code"] != "rate_limit_exceeded" {
		t.Errorf("a full server: %d %s", rec.Code, rec.Body)
	}
	if spawned.Load() != 1 {
		t.Errorf("%d turns started, want only the one that holds the slot", spawned.Load())
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("the request that held the slot: %d", code)
	}
}

// An image request is a prompt: its body is capped at 64 KiB, or at the configured
// limit when that is smaller.
func TestImagesBodyIsCappedAt64KiB(t *testing.T) {
	log := &execLog{}
	h := newHarness(t, log.imageExec("a.png", onePNG("a.png")))
	secret := h.key(t, "default", "app", false)

	rec := postImages(h, anyPolicy, secret, `{"prompt":"`+strings.Repeat("a", 70<<10)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge || decodeErrorBody(t, rec)["code"] != "request_too_large" {
		t.Errorf("a 70 KiB prompt: %d %s", rec.Code, rec.Body.String()[:min(rec.Body.Len(), 200)])
	}
	if rec := postImages(h, anyPolicy, secret, `{"prompt":"`+strings.Repeat("a", 60<<10)+`"}`); rec.Code != http.StatusOK {
		t.Errorf("a 60 KiB prompt: %d", rec.Code)
	}

	small := newHarness(t, log.imageExec("a.png", onePNG("a.png")), func(_ *Deps, c *Config) { c.BodyLimit = 200 })
	rec = postImages(small, anyPolicy, small.key(t, "default", "app", false), `{"prompt":"`+strings.Repeat("a", 500)+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over the configured limit: %d", rec.Code)
	}
}

// A client that leaves while the runtime works gets nothing, and the log says 499.
func TestImagesAClientThatLeavesIsNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHarness(t, func(c context.Context, o monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		_ = os.WriteFile(filepath.Join(o.Cwd, "a.png"), pngBytes, 0o600)
		cancel() // the client hangs up
		<-c.Done()
		return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	})
	secret := h.key(t, "default", "app", false)

	r := httptest.NewRequest(http.MethodPost, imagesURL, strings.NewReader(`{"prompt":"x"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+secret)
	rec := h.do(anyPolicy, r)

	if rec.Body.Len() != 0 {
		t.Errorf("the client left, so nothing is answered: %s", rec.Body)
	}
	if lines := strings.Join(h.logged(), "\n"); !strings.Contains(lines, "status=499") {
		t.Errorf("the log must say the client left: %q", lines)
	}
}
