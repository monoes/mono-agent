package openaiapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// The sandbox a model's class depends on is required for a sandboxed model, so that
// a sandbox that cannot be applied refuses the turn instead of running it
// unconfined; an unconfined model has none to require.
func TestImagesRequireTheSandboxOnlyOfASandboxedModel(t *testing.T) {
	log := &execLog{}
	h := newHarness(t, log.imageExec("a.png", onePNG("a.png")))
	secret := h.key(t, "default", "app", false)
	for _, model := range []string{"codex", "antigravity"} {
		if rec := postImages(h, anyPolicy, secret, `{"model":"`+model+`","prompt":"x"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", model, rec.Code, rec.Body)
		}
	}
	if log.count() != 2 || !log.opts[0].RequireSandbox || log.opts[1].RequireSandbox {
		t.Errorf("codex is sandboxed and antigravity is not: %v", []bool{log.opts[0].RequireSandbox, log.opts[1].RequireSandbox})
	}
}

// The class is checked at the start of the turn against the policy of the key: for a
// key created with --context that is the context maximum, not the listener's.
func TestImagesATurnIsHeldToTheContextCapWhenItStarts(t *testing.T) {
	h := newHarness(t, scriptedExec(evStart(false, "none"), evText("a.png"), evResult("a.png", monomind.StopEndTurn), evDone(0)))
	raised := Policy{Max: Unconfined, ContextMax: Sandboxed}
	// codex is classified sandboxed, and reports no confinement at all when it starts.
	if rec := postImages(h, raised, h.key(t, "default", "notes", true), `{"model":"codex","prompt":"x"}`); rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" {
		t.Errorf("a context key: %d %s", rec.Code, rec.Body)
	}
	// The same turn under a plain key is the listener's to judge, and the listener serves everything.
	if rec := postImages(h, raised, h.key(t, "default", "app", false), `{"model":"codex","prompt":"x"}`); rec.Code == http.StatusForbidden {
		t.Errorf("a plain key was held to the context cap: %s", rec.Body)
	}
}

// Two requests that run together work in different folders, or each would empty the
// other's files.
func TestImagesRequestsAtTheSameTimeWorkInDifferentSlotFolders(t *testing.T) {
	var mu sync.Mutex
	var cwds []string
	var started atomic.Int32
	barrier := make(chan struct{})
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		mu.Lock()
		cwds = append(cwds, o.Cwd)
		mu.Unlock()
		if started.Add(1) == 2 {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-time.After(5 * time.Second):
		}
		return imageTurn("a.png", onePNG("a.png"))(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)

	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- postImages(h, anyPolicy, secret, `{"prompt":"x"}`).Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Errorf("status %d", code)
		}
	}
	if len(cwds) != 2 || cwds[0] == cwds[1] {
		t.Errorf("two turns at once must not share a folder: %v", cwds)
	}
}

// A slot is given back when the request ends, whatever way it ends.
func TestImagesGiveTheSlotBack(t *testing.T) {
	h := newHarness(t, imageTurn("a.png", onePNG("a.png")), func(_ *Deps, c *Config) { c.MaxConcurrent = 1 })
	secret := h.key(t, "default", "app", false)
	for i, body := range []string{`{"prompt":"x"}`, `{"prompt":"x"}`, `{"prompt":"x"}`} {
		if rec := postImages(h, anyPolicy, secret, body); rec.Code != http.StatusOK {
			t.Fatalf("request %d: %d %s: the slot of the one before was not given back", i+1, rec.Code, rec.Body)
		}
	}
}

// An image turn takes a minute: the server's write timeout, which the legacy HTTP
// API sets, must not cut the response of one that runs longer than it.
func TestImagesResponseOutlivesTheServersWriteTimeout(t *testing.T) {
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		time.Sleep(400 * time.Millisecond)
		return imageTurn("a.png", onePNG("a.png"))(ctx, o, onEvent)
	})
	mux := http.NewServeMux()
	h.g.Mount(mux, anyPolicy)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+imagesURL, strings.NewReader(`{"prompt":"x"}`))
	req.Header.Set("Authorization", "Bearer "+h.key(t, "default", "app", false))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the server's WriteTimeout cut the response: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"b64_json"`) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
}

// The turn's folder is handed to Collect once, after a turn that ended cleanly and as
// the turn left it, and is emptied after that.
func TestRunTurnHandsTheFolderToCollectBeforeItIsEmptied(t *testing.T) {
	h := newHarness(t, imageTurn("a.png", onePNG("a.png")))
	var dir string
	var calls int
	var then []string
	_, err := h.g.runTurn(context.Background(), turn{Runtime: "codex", Model: "default", Prompt: "p", Policy: anyPolicy, ProfileID: "alice",
		Collect: func(d string) { calls, dir, then = calls+1, d, besidesTmp(d) }})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(then) != 1 || then[0] != "a.png" {
		t.Fatalf("Collect ran %d times and saw %v: it must run once, with the file the turn saved", calls, then)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the folder must be emptied after Collect: %d entries", len(entries))
	}
}

// What a turn that did not end cleanly left is nobody's to read.
func TestRunTurnDoesNotCollectAfterATurnThatDidNotEndCleanly(t *testing.T) {
	for name, c := range map[string]struct {
		exec   execFunc
		policy Policy
	}{
		"Exec failed to start": {func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
			return nil, errors.New("boom")
		}, anyPolicy},
		"the turn reported an error":              {scriptedExec(evStart(false, "workspace-write"), evError(monomind.ErrQuota, "limit"), evDone(1)), anyPolicy},
		"the turn started weaker than the policy": {scriptedExec(evStart(false, "none"), evText("x"), evResult("x", monomind.StopEndTurn), evDone(0)), Policy{Max: Sandboxed}},
	} {
		h := newHarness(t, c.exec)
		collected := false
		_, _ = h.g.runTurn(context.Background(), turn{Runtime: "codex", Model: "default", Prompt: "p", Policy: c.policy, Collect: func(string) { collected = true }})
		if collected {
			t.Errorf("%s: the folder was handed to Collect", name)
		}
	}
	// A turn that no one asked to collect from is run as it always was.
	h := newHarness(t, okTurn("x"))
	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy}); err != nil {
		t.Fatal(err)
	}
}
