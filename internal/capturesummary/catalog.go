package capturesummary

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Which agent runtime, and which of its models, writes a summary.
//
// The bridge has a default (--summary-runtime, else claude). A capture may
// name its own — the extension's "AI for summaries" picker stamps
// {"runtime", "model"} into meta.summarize — and Catalog is what that choice
// is checked against: the runtimes `monomind agent scan` finds installed,
// and each one's models as `monomind.ListModels` reports them (the same two
// calls the desktop app's chat box makes, see wails-app/app_ai.go). A
// runtime id a capture names is only ever used once the scan has found it;
// nothing a browser sends reaches exec unchecked.

// Runtime is one installed agent runtime.
type Runtime struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	// Binary is the runtime's resolved path. Never sent to the browser; it
	// is what codex/antigravity model discovery runs.
	Binary string `json:"-"`
}

// Model is one selectable model of a runtime (id for --model, and a label).
type Model = monomind.RuntimeModel

// ScanFunc lists the installed runtimes.
type ScanFunc func(ctx context.Context) ([]Runtime, error)

// ModelsFunc lists one installed runtime's models. Empty means the runtime
// has no list, and a model is typed by hand (or left to its default).
type ModelsFunc func(ctx context.Context, rt Runtime) ([]Model, error)

// CatalogTTL is how long a scan (and a model list) is reused. A scan
// probes every known agent CLI and can take seconds; what is installed
// changes rarely.
const CatalogTTL = 5 * time.Minute

// scanTimeout bounds one scan. It runs detached from the request that
// started it, so a caller that gives up early still leaves a warm cache.
const scanTimeout = 90 * time.Second

// ErrUnknownRuntime is a runtime id the scan did not find installed.
var ErrUnknownRuntime = errors.New("agent runtime is not installed")

var (
	runtimePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// A model id is passed to the runtime as a --model value. It is argv,
	// not a shell, but a value starting with "-" would still read as a
	// flag, and nothing a model is called needs spaces or quotes.
	modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@\[\]+-]{0,127}$`)
)

// ValidRuntimeID reports whether id is shaped like a runtime id at all —
// the cheap check before a scan is consulted.
func ValidRuntimeID(id string) bool { return runtimePattern.MatchString(id) }

// ValidModel reports whether m may be passed as --model.
func ValidModel(m string) bool { return modelPattern.MatchString(m) }

// Catalog answers "which runtimes, which models", cached.
type Catalog struct {
	Scan   ScanFunc
	Models ModelsFunc
	// TTL overrides CatalogTTL (tests).
	TTL time.Duration
	// Now supplies the time; nil means time.Now.
	Now func() time.Time

	mu        sync.Mutex
	runtimes  []Runtime
	scannedAt time.Time
	flight    *scanFlight
	models    map[string]modelEntry
}

type scanFlight struct {
	done     chan struct{}
	runtimes []Runtime
	err      error
}

type modelEntry struct {
	models []Model
	at     time.Time
}

// MonomindCatalog is the production catalog: `monomind agent scan` and
// monomind.ListModels.
func MonomindCatalog() *Catalog {
	return &Catalog{
		Scan: func(ctx context.Context) ([]Runtime, error) {
			res, err := monomind.Scan(ctx)
			if err != nil {
				return nil, err
			}
			var out []Runtime
			for _, e := range res.Installed() {
				rt := Runtime{ID: e.ID}
				if e.Version != nil {
					rt.Version = *e.Version
				}
				if e.Binary != nil {
					rt.Binary = *e.Binary
				}
				out = append(out, rt)
			}
			return out, nil
		},
		Models: func(ctx context.Context, rt Runtime) ([]Model, error) {
			return monomind.ListModels(ctx, rt.ID, rt.Binary)
		},
	}
}

func (c *Catalog) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Catalog) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return CatalogTTL
}

// Runtimes returns the installed runtimes, scanning at most once per TTL.
// Concurrent callers share one scan; a caller whose ctx ends first gets
// ctx's error while the scan finishes and fills the cache for the next.
// A failed scan is not cached.
func (c *Catalog) Runtimes(ctx context.Context) ([]Runtime, error) {
	if c == nil || c.Scan == nil {
		return nil, errors.New("no runtime scanner is configured")
	}
	c.mu.Lock()
	if !c.scannedAt.IsZero() && c.now().Sub(c.scannedAt) < c.ttl() {
		out := append([]Runtime(nil), c.runtimes...)
		c.mu.Unlock()
		return out, nil
	}
	f := c.flight
	if f == nil {
		f = &scanFlight{done: make(chan struct{})}
		c.flight = f
		go c.scan(f)
	}
	c.mu.Unlock()

	select {
	case <-f.done:
		if f.err != nil {
			return nil, f.err
		}
		return append([]Runtime(nil), f.runtimes...), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Catalog) scan(f *scanFlight) {
	ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
	defer cancel()
	rts, err := c.Scan(ctx)
	var kept []Runtime
	for _, rt := range rts {
		rt.ID = strings.TrimSpace(rt.ID)
		if ValidRuntimeID(rt.ID) {
			kept = append(kept, rt)
		}
	}
	f.runtimes, f.err = kept, err

	c.mu.Lock()
	if err == nil {
		c.runtimes, c.scannedAt = kept, c.now()
		c.models = nil // a rescan may have moved a binary
	}
	c.flight = nil
	c.mu.Unlock()
	close(f.done)
}

// Lookup returns the installed runtime called id, or an error wrapping
// ErrUnknownRuntime.
func (c *Catalog) Lookup(ctx context.Context, id string) (Runtime, error) {
	id = strings.TrimSpace(id)
	if !ValidRuntimeID(id) {
		return Runtime{}, fmt.Errorf("%q: %w", id, ErrUnknownRuntime)
	}
	rts, err := c.Runtimes(ctx)
	if err != nil {
		return Runtime{}, err
	}
	for _, rt := range rts {
		if rt.ID == id {
			return rt, nil
		}
	}
	return Runtime{}, fmt.Errorf("%q: %w", id, ErrUnknownRuntime)
}

// ModelsFor lists an installed runtime's models, cached per runtime. A
// runtime with no list returns an empty, non-nil slice.
func (c *Catalog) ModelsFor(ctx context.Context, id string) ([]Model, error) {
	rt, err := c.Lookup(ctx, id)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if e, ok := c.models[rt.ID]; ok && c.now().Sub(e.at) < c.ttl() {
		c.mu.Unlock()
		return append([]Model{}, e.models...), nil
	}
	c.mu.Unlock()

	if c.Models == nil {
		return []Model{}, nil
	}
	raw, err := c.Models(ctx, rt)
	if err != nil {
		return nil, err
	}
	models := []Model{}
	for _, m := range raw {
		m.ID = strings.TrimSpace(m.ID)
		if !ValidModel(m.ID) {
			continue
		}
		if strings.TrimSpace(m.Label) == "" {
			m.Label = m.ID
		}
		models = append(models, m)
	}
	c.mu.Lock()
	if c.models == nil {
		c.models = map[string]modelEntry{}
	}
	c.models[rt.ID] = modelEntry{models: models, at: c.now()}
	c.mu.Unlock()
	return append([]Model{}, models...), nil
}
