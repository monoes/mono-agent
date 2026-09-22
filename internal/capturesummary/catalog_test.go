package capturesummary

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCatalog is a Catalog over a fake scanner and model lister that count
// how often they are asked.
type fakeCatalog struct {
	scans  atomic.Int32
	lists  atomic.Int32
	now    time.Time
	scanFn func() ([]Runtime, error)
}

func (f *fakeCatalog) catalog() *Catalog {
	return &Catalog{
		Now: func() time.Time { return f.now },
		Scan: func(ctx context.Context) ([]Runtime, error) {
			f.scans.Add(1)
			if f.scanFn != nil {
				return f.scanFn()
			}
			return []Runtime{
				{ID: "claude", Version: "2.1.3", Binary: "/usr/bin/claude"},
				{ID: "opencode", Binary: "/usr/bin/opencode"},
				{ID: "Bad Id", Binary: "/x"}, // never offered
			}, nil
		},
		Models: func(ctx context.Context, rt Runtime) ([]Model, error) {
			f.lists.Add(1)
			if rt.ID == "claude" {
				return []Model{{ID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5"}, {ID: "--evil"}, {ID: "sonnet"}}, nil
			}
			return nil, nil
		},
	}
}

func TestCatalogCachesTheScan(t *testing.T) {
	f := &fakeCatalog{now: time.Unix(1000, 0)}
	c := f.catalog()
	ctx := context.Background()

	rts, err := c.Runtimes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rts) != 2 || rts[0].ID != "claude" || rts[1].ID != "opencode" {
		t.Fatalf("runtimes = %+v, want claude and opencode only", rts)
	}
	if _, err := c.Runtimes(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.scans.Load(); n != 1 {
		t.Errorf("scanned %d times within the TTL, want 1", n)
	}
	f.now = f.now.Add(CatalogTTL + time.Second)
	if _, err := c.Runtimes(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.scans.Load(); n != 2 {
		t.Errorf("scanned %d times after the TTL, want 2", n)
	}
}

func TestCatalogSharesOneScanAcrossCallers(t *testing.T) {
	release := make(chan struct{})
	f := &fakeCatalog{now: time.Unix(1000, 0)}
	f.scanFn = func() ([]Runtime, error) {
		<-release
		return []Runtime{{ID: "claude"}}, nil
	}
	c := f.catalog()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Runtimes(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := f.scans.Load(); n != 1 {
		t.Errorf("five concurrent callers ran %d scans, want 1", n)
	}
}

// A caller that gives up still leaves the scan running, so the next one
// finds a warm cache instead of starting over.
func TestCatalogScanOutlivesAnImpatientCaller(t *testing.T) {
	release := make(chan struct{})
	f := &fakeCatalog{now: time.Unix(1000, 0)}
	f.scanFn = func() ([]Runtime, error) {
		<-release
		return []Runtime{{ID: "claude"}}, nil
	}
	c := f.catalog()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Runtimes(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
	close(release)
	if _, err := c.Runtimes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.scans.Load(); n != 1 {
		t.Errorf("scans = %d, want 1", n)
	}
}

func TestCatalogDoesNotCacheAFailedScan(t *testing.T) {
	f := &fakeCatalog{now: time.Unix(1000, 0)}
	f.scanFn = func() ([]Runtime, error) { return nil, errors.New("monomind not found") }
	c := f.catalog()
	if _, err := c.Runtimes(context.Background()); err == nil {
		t.Fatal("want the scan's error")
	}
	f.scanFn = nil
	if _, err := c.Runtimes(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestCatalogModels(t *testing.T) {
	f := &fakeCatalog{now: time.Unix(1000, 0)}
	c := f.catalog()
	ctx := context.Background()

	models, err := c.ModelsFor(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	// "--evil" would read as a flag; it is dropped. A missing label is the id.
	if len(models) != 2 || models[0].Label != "Haiku 4.5" || models[1].Label != "sonnet" {
		t.Fatalf("models = %+v", models)
	}
	if _, err := c.ModelsFor(ctx, "claude"); err != nil {
		t.Fatal(err)
	}
	if n := f.lists.Load(); n != 1 {
		t.Errorf("listed %d times, want 1 (cached)", n)
	}

	none, err := c.ModelsFor(ctx, "opencode")
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("opencode models = %#v, %v; want an empty, non-nil list", none, err)
	}

	for _, id := range []string{"codex", "../claude", "claude; rm -rf /", ""} {
		if _, err := c.ModelsFor(ctx, id); !errors.Is(err, ErrUnknownRuntime) {
			t.Errorf("ModelsFor(%q) err = %v, want ErrUnknownRuntime", id, err)
		}
	}
}

func TestValidModel(t *testing.T) {
	for _, ok := range []string{"claude-haiku-4-5-20251001", "gpt-5.1-codex", "openai/gpt-5", "claude-opus-5[1m]", "llama3:8b"} {
		if !ValidModel(ok) {
			t.Errorf("ValidModel(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "-m", "--model=x", "a b", "a;b", "$(x)", "a\nb"} {
		if ValidModel(bad) {
			t.Errorf("ValidModel(%q) = true", bad)
		}
	}
}
