package openaiapi

// The gateway tells the operator, once, with the first list of models it gets, which
// runtimes of the image list make no images here (Catalog.onFirstLoad). The catalog
// has two kinds of caller now, the gateway's (Models: the load outlives the caller)
// and a one-shot one (ModelsBound: the load ends with it, and a load whose starter
// left caches nothing). The hook is told of the first list that is cached, whoever
// loaded it, and of no other.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestCatalogFirstLoadHookIsToldOnceWhoeverLoads(t *testing.T) {
	for name, load := range map[string]func(*Catalog, context.Context) error{
		"Models":      func(c *Catalog, ctx context.Context) error { _, err := c.Models(ctx); return err },
		"ModelsBound": func(c *Catalog, ctx context.Context) error { _, err := c.ModelsBound(ctx); return err },
	} {
		t.Run(name, func(t *testing.T) {
			f := testFuncs(t)
			scan := f.Scan
			var scans atomic.Int32
			f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) { scans.Add(1); return scan(ctx) }
			c := NewCatalog(f, time.Millisecond) // the list expires at once: the next calls reload it behind them
			var told, listed atomic.Int32
			c.onFirstLoad = func(models []ModelInfo) { told.Add(1); listed.Store(int32(len(models))) }
			for range 3 {
				if err := load(c, context.Background()); err != nil {
					t.Fatal(err)
				}
				time.Sleep(10 * time.Millisecond)
				waitForRefresh(t, c)
			}
			if scans.Load() < 2 {
				t.Fatalf("%d loads: the test does not reload the list", scans.Load())
			}
			if told.Load() != 1 || listed.Load() == 0 {
				t.Errorf("the hook was told %d times of %d models over %d loads, want once, of the first list", told.Load(), listed.Load(), scans.Load())
			}
		})
	}
}

// A bound load whose starter left caches nothing, so it is not the first list: the
// hook is told when a list is cached, and of that one.
func TestCatalogFirstLoadHookIsNotToldOfALoadThatCachedNothing(t *testing.T) {
	f := testFuncs(t)
	scan := f.Scan
	scanning := make(chan struct{}, 1)
	var scans atomic.Int32
	f.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
		if scans.Add(1) == 1 { // the first load waits for its starter to leave
			scanning <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return scan(ctx)
	}
	c := NewCatalog(f, time.Minute)
	var told atomic.Int32
	c.onFirstLoad = func([]ModelInfo) { told.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	left := make(chan error, 1)
	go func() { _, err := c.ModelsBound(ctx); left <- err }()
	select {
	case <-scanning:
	case <-time.After(5 * time.Second):
		t.Fatal("the load never started")
	}
	cancel()
	if err := <-left; !errors.Is(err, context.Canceled) {
		t.Fatalf("the starter that left: %v, want context.Canceled", err)
	}
	waitForRefresh(t, c) // the abandoned load has ended
	if told.Load() != 0 {
		t.Fatalf("the hook was told of a load that cut short and cached nothing")
	}

	if _, err := c.ModelsBound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if told.Load() != 1 {
		t.Errorf("the hook was told %d times of the first list that was cached, want once", told.Load())
	}
}
