package openaiapi

// The catalog under refresh and failure: stale lists, a runtime's last good
// list, listings that run out of time or panic.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

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
