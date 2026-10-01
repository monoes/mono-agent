package openaiapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestRunTurnBuildsTheLockedDownExecOptions(t *testing.T) {
	var got monomind.ExecOptions
	var scratchExisted bool
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		got = opts
		info, err := os.Stat(opts.Cwd)
		scratchExisted = err == nil && info.IsDir()
		return okTurn("ok")(ctx, opts, onEvent)
	}, func(d *Deps, _ *Config) {
		d.Catalog.Caps = func(context.Context) (*monomind.CapabilitySet, error) {
			return monomind.NewCapabilitySet("2.22.0", monomind.CapAgentExecEffort), nil
		}
	})

	res, err := h.g.runTurn(context.Background(), turn{
		Runtime: "codex", Model: "gpt-6-astra", Effort: "high", System: "sys", Prompt: "hello", Policy: anyPolicy, ProfileID: "alice",
	})
	if err != nil || res == nil || res.ResultText != "ok" {
		t.Fatalf("runTurn = %+v, %v", res, err)
	}

	if got.Runtime != "codex" || got.Model != "gpt-6-astra" || got.Prompt != "hello" || got.SystemPrompt != "sys" {
		t.Errorf("runtime/model/prompt: %+v", got)
	}
	if got.Effort != "high" || !got.EffortFlag {
		t.Errorf("effort: %q flag=%v", got.Effort, got.EffortFlag)
	}
	if got.Bin != "/fake/monomind" {
		t.Errorf("Bin = %q", got.Bin)
	}
	if got.Sandbox != monomind.TurnSandboxMode || got.WorkspacePurpose != "api" {
		t.Errorf("sandbox %q purpose %q", got.Sandbox, got.WorkspacePurpose)
	}
	// Nothing that widens a turn beyond the text-only posture.
	if got.Access != "" || len(got.Tools) != 0 || got.OnToolCall != nil || len(got.Settings) != 0 ||
		len(got.AllowBashPrefixes) != 0 || len(got.Env) != 0 || got.RequireSandbox {
		t.Errorf("a text turn must not carry access, tools, settings, bash prefixes or env: %+v", got)
	}
	if got.Timeout != time.Minute {
		t.Errorf("Timeout = %v, want the configured turn timeout", got.Timeout)
	}

	if !scratchExisted {
		t.Error("the turn's folder did not exist while the turn ran")
	}
	if got.Cwd != filepath.Join(h.scratch, profileFolder("alice"), "slot-0") {
		t.Errorf("Cwd %q is not the profile's slot folder under %s", got.Cwd, h.scratch)
	}
	if entries, err := os.ReadDir(got.Cwd); err != nil || len(entries) != 0 {
		t.Errorf("the slot folder must be left empty (%d entries, err = %v)", len(entries), err)
	}
	if got.RequireSandbox {
		t.Error("a turn must not require the sandbox unless its class does")
	}
}

func TestRunTurnPassesRequireSandbox(t *testing.T) {
	var got monomind.ExecOptions
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		got = opts
		return okTurn("ok")(ctx, opts, onEvent)
	})
	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "codex", Model: "default", Prompt: "p", Policy: anyPolicy, RequireSandbox: true}); err != nil {
		t.Fatal(err)
	}
	if !got.RequireSandbox {
		t.Error("a sandboxed model's turn must make Exec refuse to run it unsandboxed")
	}
}

func TestRunTurnDefaultModelPassesNoModelFlag(t *testing.T) {
	var got monomind.ExecOptions
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		got = opts
		return okTurn("ok")(ctx, opts, onEvent)
	})
	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy}); err != nil {
		t.Fatal(err)
	}
	if got.Model != "" {
		t.Errorf("Model = %q, want empty for the runtime default", got.Model)
	}
	if got.Effort != "" || got.EffortFlag {
		t.Errorf("no effort was asked for: %q %v", got.Effort, got.EffortFlag)
	}
}

// A slot's folder is fixed, because agent CLIs keep per-folder session state
// that a folder per request would pile up, and it is emptied before and after
// every turn, so nothing one request leaves can reach the next.
func TestRunTurnReusesTheSlotFolderAndEmptiesItAroundEveryTurn(t *testing.T) {
	var dirs []string
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		if entries, _ := os.ReadDir(opts.Cwd); len(entries) != 0 {
			t.Errorf("a turn started in a folder that still holds %d entries", len(entries))
		}
		dirs = append(dirs, opts.Cwd)
		_ = os.WriteFile(filepath.Join(opts.Cwd, "left-behind.txt"), []byte("x"), 0o600)
		_ = os.MkdirAll(filepath.Join(opts.Cwd, "sub", "deeper"), 0o700)
		return okTurn("ok")(ctx, opts, onEvent)
	})
	for range 3 {
		if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: 0}); err != nil {
			t.Fatal(err)
		}
	}
	if len(dirs) != 3 || dirs[0] != dirs[1] || dirs[1] != dirs[2] || filepath.Base(dirs[0]) != "slot-0" {
		t.Fatalf("a slot must keep one fixed folder: %v", dirs)
	}
	if entries, _ := os.ReadDir(dirs[0]); len(entries) != 0 {
		t.Errorf("the folder must be empty after the last turn: %d entries", len(entries))
	}
}

// Two profiles never share a working folder, so nothing one profile's turn
// leaves, and none of an agent CLI's per-folder session state, reaches the other.
// A request that was between the slot and the turn when the server began to stop
// (a context key's knowledge search takes a while) must not start a process
// nobody is left to stop.
func TestRunTurnRefusesToStartOnceShutdownHasBegun(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		ran.Store(true)
		return okTurn("x")(ctx, opts, onEvent)
	})
	if !h.g.Shutdown(time.Second) {
		t.Fatal("with nothing running, Shutdown returns at once")
	}
	_, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy})
	if !errors.Is(err, errShuttingDown) {
		t.Fatalf("err = %v, want errShuttingDown", err)
	}
	if ran.Load() {
		t.Error("no process may start after Shutdown")
	}
}

func TestRunTurnKeepsEachProfilesFolderApart(t *testing.T) {
	var dirs []string
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		dirs = append(dirs, opts.Cwd)
		return okTurn("ok")(ctx, opts, onEvent)
	})
	for _, profile := range []string{"alice", "bob", "alice"} {
		if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, ProfileID: profile, Slot: 0}); err != nil {
			t.Fatal(err)
		}
	}
	if len(dirs) != 3 || dirs[0] == dirs[1] || dirs[0] != dirs[2] {
		t.Fatalf("one slot gives each profile its own folder, and the same profile the same one: %v", dirs)
	}
	for _, d := range dirs {
		if filepath.Dir(filepath.Dir(d)) != h.scratch || filepath.Base(d) != "slot-0" {
			t.Errorf("%s is not <scratch>/<profile folder>/slot-0", d)
		}
	}
}

func TestRunTurnSlotsWorkInDifferentFolders(t *testing.T) {
	var dirs []string
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		dirs = append(dirs, filepath.Base(opts.Cwd))
		return okTurn("ok")(ctx, opts, onEvent)
	})
	for slot := range 2 {
		if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: slot}); err != nil {
			t.Fatal(err)
		}
	}
	if len(dirs) != 2 || dirs[0] != "slot-0" || dirs[1] != "slot-1" {
		t.Fatalf("each slot works in its own folder: %v", dirs)
	}
}

// A runtime can leave a read-only directory behind it (Go's module cache does).
// That must not brick the slot for good: the gateway owns the folder and opens
// it up before emptying it.
func TestRunTurnRepairsAFolderALastTurnLeftReadOnly(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		ran.Store(true)
		return okTurn("ok")(ctx, opts, onEvent)
	})
	slot := filepath.Join(h.scratch, profileFolder(""), "slot-0")
	locked := filepath.Join(slot, "mod", "locked")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{locked, filepath.Join(slot, "mod"), slot} { // deepest first
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, d := range []string{slot, filepath.Join(slot, "mod"), locked} {
			_ = os.Chmod(d, 0o700)
		}
	})

	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: 0}); err != nil {
		t.Fatalf("a read-only leftover must be repaired, not refused: %v", err)
	}
	if !ran.Load() {
		t.Error("the turn must run once the folder is clean")
	}
	if entries, _ := os.ReadDir(slot); len(entries) != 0 {
		t.Errorf("the folder must be empty afterwards: %d entries", len(entries))
	}
}

// What cannot be emptied at all still refuses the turn: it must never run among
// what an earlier request left.
func TestRunTurnRefusesAFolderItCannotEmpty(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		ran.Store(true)
		return okTurn("ok")(ctx, opts, onEvent)
	})
	slot := filepath.Join(h.scratch, profileFolder(""), "slot-0")
	if err := os.MkdirAll(slot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slot, "left-behind.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	was := removeAll
	removeAll = func(string) error { return errors.New("operation not permitted") }
	t.Cleanup(func() { removeAll = was })

	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: 0}); err == nil {
		t.Fatal("a turn must not start in a folder that still holds what an earlier turn left")
	}
	if ran.Load() {
		t.Error("nothing must run when the folder is not clean")
	}
}

// A link planted where a profile folder or a slot folder should be would send
// the emptying, and the turn, somewhere else.
func TestRunTurnRefusesASymlinkedFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	for name, linkAt := range map[string]func(scratch string) string{
		"slot folder":    func(scratch string) string { return filepath.Join(scratch, profileFolder(""), "slot-0") },
		"profile folder": func(scratch string) string { return filepath.Join(scratch, profileFolder("")) },
	} {
		t.Run(name, func(t *testing.T) {
			var ran atomic.Bool
			h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
				ran.Store(true)
				return okTurn("ok")(ctx, opts, onEvent)
			})
			outside := t.TempDir()
			precious := filepath.Join(outside, "slot-0", "precious.txt")
			if err := os.MkdirAll(filepath.Dir(precious), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(precious, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := linkAt(h.scratch)
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			target := outside
			if name == "slot folder" {
				target = filepath.Join(outside, "slot-0")
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}

			if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy, Slot: 0}); err == nil {
				t.Fatal("a turn must not run in a folder that is a symlink")
			}
			if ran.Load() {
				t.Error("nothing must run")
			}
			if _, err := os.Stat(precious); err != nil {
				t.Errorf("the emptying followed the link and removed what is behind it: %v", err)
			}
		})
	}
}

func TestRunTurnStreamsDeltasOnlyFromIncrementalRuntimes(t *testing.T) {
	collect := func(streams bool) []string {
		var parts []string
		h := newHarness(t, scriptedExec(evStart(streams, "monomind"), evText("a"), evText("b"), evResult("ab", monomind.StopEndTurn), evDone(0)))
		_, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy,
			OnDelta: func(s string) { parts = append(parts, s) }})
		if err != nil {
			t.Fatal(err)
		}
		return parts
	}
	if got := collect(true); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("incremental runtime: deltas %v, want [a b]", got)
	}
	if got := collect(false); len(got) != 0 {
		t.Errorf("a runtime that does not stream incrementally must not produce deltas: %v", got)
	}
}

func TestRunTurnCancelsWhenTheStartEventIsWeakerThanThePolicy(t *testing.T) {
	var cancelled atomic.Bool
	exec := func(ctx context.Context, _ monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		onEvent(evStart(false, "none")) // the runtime turns out to be unconfined
		select {
		case <-ctx.Done():
			cancelled.Store(true)
		case <-time.After(5 * time.Second):
		}
		return &monomind.TurnResult{}, nil
	}
	h := newHarness(t, exec)

	_, err := h.g.runTurn(context.Background(), turn{Runtime: "codex", Model: "default", Prompt: "p", Policy: Policy{Max: Sandboxed}})
	if !errors.Is(err, errPolicyDenied) {
		t.Fatalf("err = %v, want errPolicyDenied", err)
	}
	if !cancelled.Load() {
		t.Error("the turn was not cancelled")
	}

	// A start event that does not report native_sandbox (an older monomind)
	// is not second-guessed: the scan-based classification already decided.
	h2 := newHarness(t, scriptedExec(evStart(false, ""), evText("x"), evResult("x", monomind.StopEndTurn), evDone(0)))
	if _, err := h2.g.runTurn(context.Background(), turn{Runtime: "codex", Model: "default", Prompt: "p", Policy: Policy{Max: ChatOnly}}); err != nil {
		t.Fatalf("a start event without native_sandbox must be accepted: %v", err)
	}
}

func TestRunTurnPropagatesBinAndExecErrors(t *testing.T) {
	boom := errors.New("monomind not found")
	h := newHarness(t, okTurn("x"), func(d *Deps, _ *Config) {
		d.Bin = func(context.Context) (string, error) { return "", boom }
	})
	if _, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the Bin error", err)
	}

	execErr := errors.New("prompt is required")
	h2 := newHarness(t, func(_ context.Context, opts monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		_ = os.WriteFile(filepath.Join(opts.Cwd, "partial.txt"), []byte("x"), 0o600)
		return nil, execErr
	})
	if _, err := h2.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy}); !errors.Is(err, execErr) {
		t.Fatalf("err = %v, want Exec's error", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(h2.scratch, profileFolder(""), "slot-0")); len(entries) != 0 {
		t.Errorf("a failed turn left %d entries in its folder", len(entries))
	}
}

func TestRunTurnDropsEventsThatArriveAfterAPolicyDenial(t *testing.T) {
	var deltas []string
	exec := func(ctx context.Context, _ monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		onEvent(evStart(true, "none")) // an unconfined runtime
		onEvent(evText("these words come from a runtime the policy refuses"))
		<-ctx.Done()
		return &monomind.TurnResult{}, nil
	}
	h := newHarness(t, exec)
	_, err := h.g.runTurn(context.Background(), turn{Runtime: "antigravity", Model: "default", Prompt: "p", Policy: Policy{Max: ChatOnly},
		OnDelta: func(s string) { deltas = append(deltas, s) }})
	if !errors.Is(err, errPolicyDenied) {
		t.Fatalf("err = %v, want errPolicyDenied", err)
	}
	if len(deltas) != 0 {
		t.Errorf("text from a denied turn reached the client: %v", deltas)
	}
}

func TestRunTurnReportsTheGatewaysOwnDeadlineAsATimeout(t *testing.T) {
	old := turnGrace
	turnGrace = 30 * time.Millisecond
	t.Cleanup(func() { turnGrace = old })

	// monomind misses its own --timeout, so the gateway's context ends the turn.
	exec := func(ctx context.Context, _ monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		<-ctx.Done()
		return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	}
	h := newHarness(t, exec, func(_ *Deps, c *Config) { c.TurnTimeout = 20 * time.Millisecond })
	res, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy})
	if err != nil || res.Err == nil || res.Err.Code != monomind.ErrTimeout {
		t.Fatalf("a turn ended by the gateway's deadline must be a timeout: %+v, %v", res, err)
	}

	// A caller who leaves is a cancellation, nobody to answer.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	h2 := newHarness(t, exec, func(_ *Deps, c *Config) { c.TurnTimeout = time.Minute })
	res, err = h2.g.runTurn(ctx, turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy})
	if err != nil || res.Err == nil || res.Err.Code != monomind.ErrCancelled {
		t.Fatalf("a caller who left must stay a cancellation: %+v, %v", res, err)
	}
}

func TestRunTurnShutdownEndsTurnsInFlightAndWaitsForThem(t *testing.T) {
	started := make(chan struct{})
	var ended atomic.Bool
	h := newHarness(t, func(ctx context.Context, _ monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		close(started)
		<-ctx.Done() // Exec cancels the turn
		time.Sleep(100 * time.Millisecond)
		ended.Store(true)
		return &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	})
	go h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy})
	<-started

	if !h.g.Shutdown(5 * time.Second) {
		t.Fatal("Shutdown must report that the turn is gone")
	}
	if !ended.Load() {
		t.Fatal("Shutdown returned while the turn was still running")
	}
}

func TestRunTurnDrainWaitsForTurnsInFlight(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		close(started)
		<-release
		return okTurn("x")(ctx, o, onEvent)
	})
	if !h.g.Drain(time.Second) {
		t.Fatal("with nothing running, Drain returns at once")
	}
	go h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy})
	<-started
	if h.g.Drain(50 * time.Millisecond) {
		t.Fatal("Drain must not report done while a turn runs")
	}
	done := make(chan bool, 1)
	go func() { done <- h.g.Drain(5 * time.Second) }()
	close(release)
	if !<-done {
		t.Fatal("Drain must return once the turn has ended")
	}
}
