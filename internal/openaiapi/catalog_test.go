package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/monomind"
)

// testFuncs returns catalog lookups backed by the captured scan: claude,
// codex, antigravity and hermes installed, grok not.
func testFuncs(t *testing.T) CatalogFuncs {
	t.Helper()
	scan := loadScanFixture(t)
	scan.Agents = append(scan.Agents, monomind.ScanEntry{ID: "grok", Installed: false})
	return CatalogFuncs{
		Scan: func(context.Context) (*monomind.ScanResult, error) { return scan, nil },
		Models: func(_ context.Context, runtime, _ string) ([]monomind.RuntimeModel, error) {
			switch runtime {
			case "claude":
				return []monomind.RuntimeModel{
					{ID: "default", Label: "Default"},
					{ID: "opus[1m]", Label: "Opus (1M context)", EffortLevels: []string{"low", "high"}},
					{ID: "opus", Label: "Opus", AliasOf: "opus[1m]"},
				}, nil
			case "codex":
				return []monomind.RuntimeModel{{ID: "gpt-6-astra", Label: "GPT-6-Astra"}}, nil
			case "antigravity":
				return []monomind.RuntimeModel{{ID: "gemini-3.8-flash-high", Label: "Gemini 3.8 Flash (High)"}}, nil
			}
			return nil, nil // a runtime without a listing command: monomind.ListModels answers nil, nil
		},
		Caps: func(context.Context) (*monomind.CapabilitySet, error) {
			return monomind.NewCapabilitySet("2.22.0", monomind.CapAgentExecSandbox), nil
		},
		Roster: func(context.Context) ([]agentroster.Result, error) { return nil, nil },
	}
}

// waitForRefresh waits for the background reload an expired list starts.
func waitForRefresh(t *testing.T, c *Catalog) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		idle := c.flight == nil
		c.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the background reload did not finish")
}

func ids(models []ModelInfo) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func TestCatalogListsInstalledRuntimesAndAlwaysOffersDefault(t *testing.T) {
	c := NewCatalog(testFuncs(t), time.Minute)
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"claude/default", "claude/opus[1m]", "claude/opus",
		"codex/default", "codex/gpt-6-astra",
		"antigravity/default", "antigravity/gemini-3.8-flash-high",
		"hermes/default", // its listing failed: the default model is still offered
	}
	got := ids(models)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	byID := map[string]ModelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}
	if m := byID["claude/opus[1m]"]; m.Class != ChatOnly || m.Runtime != "claude" || m.Model != "opus[1m]" || m.Label != "Opus (1M context)" || len(m.Efforts) != 2 {
		t.Errorf("claude/opus[1m] = %+v", m)
	}
	if byID["codex/gpt-6-astra"].Class != Sandboxed || byID["antigravity/default"].Class != Unconfined {
		t.Errorf("classes: codex %v, antigravity %v", byID["codex/gpt-6-astra"].Class, byID["antigravity/default"].Class)
	}
	if !byID["claude/opus"].Alias || byID["claude/opus[1m]"].Alias {
		t.Errorf("alias flags wrong: %+v / %+v", byID["claude/opus"], byID["claude/opus[1m]"])
	}
}

func TestCatalogFetchesRuntimeListsInParallel(t *testing.T) {
	f := testFuncs(t)
	var started atomic.Int32
	f.Models = func(ctx context.Context, runtime, _ string) ([]monomind.RuntimeModel, error) {
		started.Add(1)
		deadline := time.After(3 * time.Second)
		for started.Load() < 4 { // the four installed runtimes must all be in flight at once
			select {
			case <-deadline:
				return nil, errors.New("listings are running one after another")
			case <-time.After(time.Millisecond):
			}
		}
		return []monomind.RuntimeModel{{ID: "m-" + runtime}}, nil
	}
	c := NewCatalog(f, time.Minute)
	begin := time.Now()
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("listing took %v: not parallel", took)
	}
	for _, m := range models {
		if m.Model == "m-claude" {
			return
		}
	}
	t.Fatalf("a runtime's own listing was dropped: %v", ids(models))
}

func TestCatalogCachesAndCoalescesConcurrentLoads(t *testing.T) {
	f := testFuncs(t)
	var scans atomic.Int32
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		scans.Add(1)
		time.Sleep(50 * time.Millisecond)
		return scan(ctx)
	}
	c := NewCatalog(f, time.Minute)
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Models(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := scans.Load(); got != 1 {
		t.Fatalf("ten concurrent callers triggered %d scans, want 1", got)
	}

	if _, err := c.Models(context.Background()); err != nil || scans.Load() != 1 {
		t.Fatalf("a call within the TTL must be served from the cache (scans=%d, err=%v)", scans.Load(), err)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := c.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForRefresh(t, c)
	if scans.Load() != 2 {
		t.Fatalf("a call after the TTL must reload, in the background (scans=%d)", scans.Load())
	}
}

func TestCatalogServesStaleWhenAReloadFails(t *testing.T) {
	f := testFuncs(t)
	var logged []string
	f.Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	c := NewCatalog(f, 5*time.Minute)
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }
	if _, err := c.Models(context.Background()); err != nil {
		t.Fatal(err)
	}

	var scans atomic.Int32
	c.f.Scan = func(context.Context) (*monomind.ScanResult, error) {
		scans.Add(1)
		return nil, errors.New("monomind is gone")
	}
	clock = clock.Add(time.Hour)
	models, err := c.Models(context.Background())
	if err != nil || len(models) == 0 {
		t.Fatalf("an expired list must be served while it reloads: %d models, %v", len(models), err)
	}
	waitForRefresh(t, c)
	if len(logged) != 1 || !strings.Contains(logged[0], "monomind is gone") {
		t.Errorf("a failed refresh must be logged once: %q", logged)
	}
	if models, err := c.Models(context.Background()); err != nil || len(models) == 0 {
		t.Fatalf("a failed reload must fall back to the cached list: %d models, %v", len(models), err)
	}

	// The broken monomind is tried again soon, not only after a whole TTL, and
	// not on every request either.
	clock = clock.Add(10 * time.Second)
	if _, err := c.Models(context.Background()); err != nil || scans.Load() != 1 {
		t.Fatalf("within the retry delay the stale list is served without a rescan (scans=%d, err=%v)", scans.Load(), err)
	}
	clock = clock.Add(40 * time.Second)
	if _, err := c.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForRefresh(t, c)
	if scans.Load() != 2 {
		t.Fatalf("after the retry delay the reload is tried again (scans=%d)", scans.Load())
	}

	// With nothing cached the error surfaces.
	cold := NewCatalog(CatalogFuncs{
		Scan:   func(context.Context) (*monomind.ScanResult, error) { return nil, errors.New("monomind is gone") },
		Models: f.Models, Caps: f.Caps, Roster: f.Roster,
	}, time.Minute)
	if _, err := cold.Models(context.Background()); err == nil {
		t.Fatal("expected the scan error when nothing is cached")
	}
}

func TestCatalogMarksModelsTheRosterValidated(t *testing.T) {
	f := testFuncs(t)
	f.Roster = func(context.Context) ([]agentroster.Result, error) {
		return []agentroster.Result{
			{Runtime: "claude", Model: "opus[1m]", Status: agentroster.StatusOK, ValidatedAt: time.Now()},
			{Runtime: "codex", Model: "gpt-6-astra", Status: agentroster.StatusAuth, ValidatedAt: time.Now()},
		}, nil
	}
	models, err := NewCatalog(f, time.Minute).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	validated := map[string]bool{}
	for _, m := range models {
		validated[m.ID] = m.Validated
	}
	if !validated["claude/opus[1m]"] || validated["codex/gpt-6-astra"] || validated["claude/default"] {
		t.Errorf("validated flags: %v", validated)
	}
}

func TestCatalogResolve(t *testing.T) {
	c := NewCatalog(testFuncs(t), time.Minute)
	ctx := context.Background()

	for name, want := range map[string]string{
		"claude/opus[1m]":                   "claude/opus[1m]",
		"claude/default":                    "claude/default",
		"claude":                            "claude/default", // a bare runtime means its default model
		"codex":                             "codex/default",
		"agy/gemini-3.8-flash-high":         "antigravity/gemini-3.8-flash-high",
		"agy":                               "antigravity/default",
		"antigravity/gemini-3.8-flash-high": "antigravity/gemini-3.8-flash-high",
		"Claude/default":                    "claude/default", // runtime ids are case-insensitive
		"claude/opus":                       "claude/opus",    // an alias id still resolves
	} {
		got, err := c.Resolve(ctx, name)
		if err != nil || got.ID != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", name, got.ID, err, want)
		}
	}

	// Everything else is "not found", including every shape that could be
	// mistaken for a command line flag by the runner.
	for _, bad := range []string{
		"", "/", "claude/", "/default", "nope/default", "claude/does-not-exist",
		"-x/default", "claude/--help", "claude/--model x", "claude/opus[1m] --x",
		"claude/default\n", "claude /default", "claude/$(id)", "claude/;ls", "grok/default", "auto",
		string(make([]byte, 300)),
	} {
		if got, err := c.Resolve(ctx, bad); !errors.Is(err, ErrUnknownModel) {
			t.Errorf("Resolve(%q) = %+v, %v; want ErrUnknownModel", bad, got, err)
		}
	}
}

func TestCatalogVisibleAppliesThePolicyAndHidesAliases(t *testing.T) {
	c := NewCatalog(testFuncs(t), time.Minute)
	ctx := context.Background()

	chat, _ := c.Visible(ctx, Policy{Max: ChatOnly})
	for _, m := range chat {
		if m.Runtime != "claude" || m.Alias {
			t.Errorf("chat-only policy listed %s (alias=%v)", m.ID, m.Alias)
		}
	}
	if len(chat) != 2 { // claude/default and claude/opus[1m]; claude/opus is an alias
		t.Errorf("chat-only listed %v", ids(chat))
	}
	sand, _ := c.Visible(ctx, Policy{Max: Sandboxed})
	if len(sand) != 4 { // + codex/default and codex/gpt-6-astra
		t.Errorf("sandboxed listed %v", ids(sand))
	}
	all, _ := c.Visible(ctx, Policy{Max: Unconfined})
	if len(all) != 7 { // every non-alias model
		t.Errorf("any listed %v", ids(all))
	}
}

func TestCatalogOffersRosterModelsTheRuntimeDoesNotList(t *testing.T) {
	f := testFuncs(t)
	f.Roster = func(context.Context) ([]agentroster.Result, error) {
		return []agentroster.Result{
			{Runtime: "codex", Model: "my-custom-model", Label: "my-custom-model", Status: agentroster.StatusUntested, Source: agentroster.SourceManual},
			{Runtime: "grok", Model: "grok-9", Status: agentroster.StatusOK},              // not installed: ignored
			{Runtime: "codex", Model: "--dangerously-skip", Status: agentroster.StatusOK}, // not a model id: ignored
		}, nil
	}
	c := NewCatalog(f, time.Minute)
	got, err := c.Resolve(context.Background(), "codex/my-custom-model")
	if err != nil || got.Class != Sandboxed {
		t.Fatalf("a roster model of an installed runtime must resolve with its runtime's class: %+v, %v", got, err)
	}
	for _, name := range []string{"grok/grok-9", "codex/--dangerously-skip"} {
		if _, err := c.Resolve(context.Background(), name); !errors.Is(err, ErrUnknownModel) {
			t.Errorf("%s must stay unknown, got %v", name, err)
		}
	}
}

func TestCatalogCachesAnEmptyList(t *testing.T) {
	var scans atomic.Int32
	f := testFuncs(t)
	f.Scan = func(context.Context) (*monomind.ScanResult, error) {
		scans.Add(1)
		return &monomind.ScanResult{}, nil // no runtime installed
	}
	c := NewCatalog(f, time.Minute)
	for range 3 {
		if models, err := c.Models(context.Background()); err != nil || len(models) != 0 {
			t.Fatalf("models = %v, %v", ids(models), err)
		}
	}
	if scans.Load() != 1 {
		t.Fatalf("an empty list is a result too: %d scans for three calls, want 1", scans.Load())
	}
}

func TestCatalogASlowRuntimeListingCostsOnlyItsOwnModels(t *testing.T) {
	old := listTimeout
	listTimeout = 50 * time.Millisecond
	t.Cleanup(func() { listTimeout = old })

	f := testFuncs(t)
	models := f.Models
	f.Models = func(ctx context.Context, runtime, bin string) ([]monomind.RuntimeModel, error) {
		if runtime == "codex" {
			<-ctx.Done() // a hung listing command
			return nil, ctx.Err()
		}
		return models(ctx, runtime, bin)
	}
	begin := time.Now()
	got, err := NewCatalog(f, time.Minute).Models(context.Background())
	if err != nil || time.Since(begin) > 3*time.Second {
		t.Fatalf("a hung listing must not stall the list: %v after %v", err, time.Since(begin))
	}
	have := map[string]bool{}
	for _, m := range got {
		have[m.ID] = true
	}
	if !have["codex/default"] || have["codex/gpt-6-astra"] || !have["claude/default"] {
		t.Errorf("codex keeps only its default model, the others are untouched: %v", ids(got))
	}
}

// An expired list is served at once while the reload runs: a scan that hangs
// must not stall every request for its whole timeout.
func TestCatalogServesTheCachedListWhileItRefreshes(t *testing.T) {
	f := testFuncs(t)
	release := make(chan struct{})
	var slow atomic.Bool
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		if slow.Load() {
			<-release
		}
		return scan(ctx)
	}
	c := NewCatalog(f, time.Minute)
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }
	first, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	slow.Store(true)
	clock = clock.Add(2 * time.Minute)
	begin := time.Now()
	got, err := c.Models(context.Background())
	if err != nil || len(got) != len(first) || time.Since(begin) > time.Second {
		t.Fatalf("an expired list must be served at once while the reload runs: %d models, %v after %v", len(got), err, time.Since(begin))
	}
	close(release)
	waitForRefresh(t, c)
}

// A runtime whose own listing fails at a refresh keeps the models it had: one
// bad listing must not turn a model clients use into a 404 for a whole TTL.
func TestCatalogKeepsARuntimesLastGoodListWhenItsListingFailsAtARefresh(t *testing.T) {
	f := testFuncs(t)
	var codexDown atomic.Bool
	models := f.Models
	f.Models = func(ctx context.Context, runtime, bin string) ([]monomind.RuntimeModel, error) {
		if runtime == "codex" && codexDown.Load() {
			return nil, errors.New("codex debug models failed")
		}
		return models(ctx, runtime, bin)
	}
	var scans atomic.Int32
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		scans.Add(1)
		return scan(ctx)
	}
	c := NewCatalog(f, 5*time.Minute)
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }
	ctx := context.Background()
	if _, err := c.Resolve(ctx, "codex/gpt-6-astra"); err != nil {
		t.Fatal(err)
	}

	codexDown.Store(true)
	clock = clock.Add(10 * time.Minute)
	_, _ = c.Models(ctx) // expired: the reload starts in the background
	waitForRefresh(t, c)
	if _, err := c.Resolve(ctx, "codex/gpt-6-astra"); err != nil {
		t.Fatalf("a runtime whose listing failed at a refresh must keep its last good models: %v", err)
	}

	// The degraded list is not kept for a whole TTL either: it is tried again soon.
	before := scans.Load()
	clock = clock.Add(40 * time.Second)
	_, _ = c.Models(ctx)
	waitForRefresh(t, c)
	if scans.Load() != before+1 {
		t.Errorf("a refresh that fell back must be retried within seconds: scans %d -> %d", before, scans.Load())
	}
}

// monomind.ListModels answers a listing that ran out of time with its built-in
// fallback and no error, so a timeout counts as a failure like an error does:
// the fallback must not replace the live list.
func TestCatalogTreatsAListingThatRanOutOfTimeAsFailed(t *testing.T) {
	old := listTimeout
	listTimeout = 50 * time.Millisecond
	t.Cleanup(func() { listTimeout = old })

	f := testFuncs(t)
	models := f.Models
	var hung atomic.Bool
	f.Models = func(ctx context.Context, runtime, bin string) ([]monomind.RuntimeModel, error) {
		if runtime == "claude" && hung.Load() {
			<-ctx.Done() // the live listing hangs, then the built-in fallback answers
			return []monomind.RuntimeModel{{ID: "claude-sonnet-5"}}, nil
		}
		return models(ctx, runtime, bin)
	}
	c := NewCatalog(f, time.Minute)
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }
	ctx := context.Background()
	if _, err := c.Resolve(ctx, "claude/opus[1m]"); err != nil {
		t.Fatal(err)
	}

	hung.Store(true)
	clock = clock.Add(2 * time.Minute)
	_, _ = c.Models(ctx)
	waitForRefresh(t, c)
	if _, err := c.Resolve(ctx, "claude/opus[1m]"); err != nil {
		t.Errorf("the fallback of a listing that timed out replaced the live list: %v", err)
	}
	if _, err := c.Resolve(ctx, "claude/claude-sonnet-5"); err == nil {
		t.Error("the fallback ids must not appear next to the live list")
	}
}

// A panic in one runtime's listing must not take the process down: the listings
// run in goroutines of their own.
func TestCatalogAListingThatPanicsOnlyCostsItsRuntime(t *testing.T) {
	f := testFuncs(t)
	models := f.Models
	f.Models = func(ctx context.Context, runtime, bin string) ([]monomind.RuntimeModel, error) {
		if runtime == "codex" {
			panic("listing blew up")
		}
		return models(ctx, runtime, bin)
	}
	got, err := NewCatalog(f, time.Minute).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, m := range got {
		have[m.ID] = true
	}
	if !have["codex/default"] || have["codex/gpt-6-astra"] || !have["claude/default"] {
		t.Errorf("codex keeps only its default model, the others are untouched: %v", ids(got))
	}
}

func TestCatalogALoadThatPanicsDoesNotWedgeLaterCallers(t *testing.T) {
	f := testFuncs(t)
	var calls atomic.Int32
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		if calls.Add(1) == 1 {
			panic("scan blew up")
		}
		return scan(ctx)
	}
	c := NewCatalog(f, time.Minute)

	func() {
		defer func() { _ = recover() }() // net/http recovers a handler's panic the same way
		_, _ = c.Models(context.Background())
	}()

	done := make(chan error, 1)
	go func() {
		_, err := c.Models(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the next call must load normally: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a call after a panicking load is stuck behind the dead leader")
	}
}
