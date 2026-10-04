package openaiapi

// What a one-shot caller of the catalog can count on (ModelsBound): the load it
// starts ends with it, and it stops waiting when it leaves. The gateway's callers
// (Models) keep the opposite on purpose: their load outlives them, because others
// wait on it, and a test here pins that it still does.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// releasable returns a channel that is closed when the test ends, for a fake that
// would otherwise wait for a context that never ends (a goroutine left behind
// would outlive the test).
func releasable(t *testing.T) <-chan struct{} {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return release
}

// The gateway relies on a load outliving the caller that started it: the callers
// that arrive meanwhile wait on it, and the list is cached for the next ones. This
// holds for Models and must go on holding.
func TestCatalogLoadOutlivesItsCaller(t *testing.T) {
	f := testFuncs(t)
	var scans atomic.Int32
	var endedCancelled atomic.Bool
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		scans.Add(1)
		time.Sleep(200 * time.Millisecond)
		endedCancelled.Store(ctx.Err() != nil)
		return scan(ctx)
	}
	c := NewCatalog(f, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel) // the first caller leaves while the scan runs
	models, err := c.Models(ctx)
	if err != nil || len(models) == 0 {
		t.Fatalf("the load must finish for the callers that wait on it: %d models, %v", len(models), err)
	}
	if endedCancelled.Load() {
		t.Error("the load was cancelled with the caller that started it")
	}
	if _, err := c.Models(context.Background()); err != nil || scans.Load() != 1 {
		t.Errorf("the list must be cached for the next caller (scans=%d, err=%v)", scans.Load(), err)
	}
}

func TestCatalogBoundCallersShareOneLoad(t *testing.T) {
	f := testFuncs(t)
	var scans atomic.Int32
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		scans.Add(1)
		time.Sleep(100 * time.Millisecond)
		return scan(ctx)
	}
	c := NewCatalog(f, time.Minute)

	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.ModelsBound(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := c.ModelsBound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := scans.Load(); got != 1 {
		t.Fatalf("thirteen callers started %d scans, want 1: one shared load, then the cache", got)
	}
}

// The load of a bound caller ends with it: a scan that watches its context is
// cancelled, and the caller is not kept waiting for a command that does not
// (a killed shell leaves a child holding the pipe, and the call goes on reading
// until that child ends).
func TestCatalogBoundLoadEndsWithItsCaller(t *testing.T) {
	for _, c := range []struct {
		name          string
		watchesCtx    bool
		wantCancelled bool
	}{
		{"a scan that watches its context", true, true},
		{"a scan that does not", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			release := releasable(t)
			f := testFuncs(t)
			started := make(chan struct{}, 4)
			var sawCancel atomic.Bool
			f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
				started <- struct{}{}
				if !c.watchesCtx {
					<-release
					return nil, errors.New("released")
				}
				select {
				case <-ctx.Done():
					sawCancel.Store(true)
					return nil, ctx.Err()
				case <-release:
					return nil, errors.New("released")
				}
			}
			cat := NewCatalog(f, time.Minute)

			ctx, cancel := context.WithCancel(context.Background())
			errc := make(chan error, 1)
			go func() {
				_, err := cat.ModelsBound(ctx)
				errc <- err
			}()
			<-started
			cancel()
			select {
			case err := <-errc:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("a caller that left: err = %v, want context.Canceled", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the call is still waiting for its load after its context ended")
			}
			if c.wantCancelled {
				deadline := time.Now().Add(2 * time.Second)
				for !sawCancel.Load() && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				if !sawCancel.Load() {
					t.Error("the scan was not cancelled with the caller that started it")
				}
			}
		})
	}
}

// A load that the cancellation cut short can still look like a success: a runtime
// whose listing failed because of it keeps its default model, and the load goes on.
// It must not be cached, or the next caller would be served a list with every
// runtime reduced to its default.
func TestCatalogBoundLoadCancelledDuringTheListingsCachesNothing(t *testing.T) {
	old := listTimeout
	listTimeout = 200 * time.Millisecond
	t.Cleanup(func() { listTimeout = old })

	f := testFuncs(t)
	var scans atomic.Int32
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		scans.Add(1)
		return scan(ctx)
	}
	listing := make(chan struct{}, 8)
	var cut atomic.Bool
	cut.Store(true)
	models := f.Models
	f.Models = func(ctx context.Context, runtime, bin string) ([]monomind.RuntimeModel, error) {
		if cut.Load() {
			listing <- struct{}{}
			<-ctx.Done() // the cancellation arrives in the middle of the listings
			return nil, ctx.Err()
		}
		return models(ctx, runtime, bin)
	}
	c := NewCatalog(f, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.ModelsBound(ctx)
		errc <- err
	}()
	<-listing
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a caller that left during the listings: err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the call is still waiting for its load after its context ended")
	}

	cut.Store(false)
	got, err := c.ModelsBound(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, m := range got {
		have[m.ID] = true
	}
	if scans.Load() != 2 || !have["codex/gpt-6-astra"] || !have["claude/opus[1m]"] {
		t.Errorf("the next caller must get a load of its own, not what the cancelled one left (scans=%d): %v", scans.Load(), ids(got))
	}
}

// Callers share a load, and a bound load ends with the caller that started it. One
// that joined it and is still there must not be handed that caller's cancellation:
// it starts a load of its own.
func TestCatalogBoundCallerWhoseStarterLeftLoadsItself(t *testing.T) {
	release := releasable(t)
	f := testFuncs(t)
	var scans atomic.Int32
	started := make(chan struct{}, 1)
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		if scans.Add(1) == 1 { // the starter's load, until it leaves
			started <- struct{}{}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return nil, errors.New("released")
			}
		}
		return scan(ctx)
	}
	c := NewCatalog(f, time.Minute)

	starterCtx, leave := context.WithCancel(context.Background())
	starterErr := make(chan error, 1)
	go func() {
		_, err := c.ModelsBound(starterCtx)
		starterErr <- err
	}()
	<-started
	type result struct {
		models []ModelInfo
		err    error
	}
	joiner := make(chan result, 1)
	go func() {
		m, err := c.ModelsBound(context.Background())
		joiner <- result{m, err}
	}()
	time.Sleep(100 * time.Millisecond) // the joiner is waiting on the starter's load
	leave()

	if err := <-starterErr; !errors.Is(err, context.Canceled) {
		t.Errorf("the starter: err = %v, want context.Canceled", err)
	}
	select {
	case r := <-joiner:
		if r.err != nil || len(r.models) == 0 {
			t.Errorf("a caller that stayed must get a list, not the starter's cancellation: %d models, %v", len(r.models), r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the caller that joined is stuck behind the starter that left")
	}
	if scans.Load() != 2 {
		t.Errorf("the caller that stayed loads once for itself (scans=%d, want 2)", scans.Load())
	}
}

// A bound load runs on a goroutine of its own, where a panic would end the process.
func TestCatalogBoundLoadThatPanicsIsAnErrorAndDoesNotWedgeLaterCallers(t *testing.T) {
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

	var err error
	func() {
		defer func() { _ = recover() }()
		_, err = c.ModelsBound(context.Background())
	}()
	if err == nil {
		t.Fatal("a load that panicked must come back as an error")
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.ModelsBound(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the next call must load normally: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a call after a panicking load is stuck behind the dead one")
	}
}

// A list that is already cached is served as the gateway serves it, whoever asks:
// at once, an expired one while the reload runs in the background.
func TestCatalogBoundCallerGetsTheCachedListAtOnce(t *testing.T) {
	release := releasable(t)
	f := testFuncs(t)
	var slow atomic.Bool
	scan := f.Scan
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		if slow.Load() {
			<-release
		}
		return scan(ctx)
	}
	c := NewCatalog(f, time.Minute)
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return clock }
	first, err := c.ModelsBound(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	slow.Store(true)
	clock = clock.Add(2 * time.Minute)
	begin := time.Now()
	got, err := c.ModelsBound(context.Background())
	if err != nil || len(got) != len(first) || time.Since(begin) > time.Second {
		t.Fatalf("an expired list must be served at once while it reloads: %d models, %v after %v", len(got), err, time.Since(begin))
	}
}
