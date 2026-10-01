package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/monomind"
)

// ErrUnknownModel means a model name does not resolve to a catalog entry.
var ErrUnknownModel = errors.New("unknown model")

// aliases are alternative runtime names accepted on input. The canonical id
// always uses the runtime id monomind reports.
var aliases = map[string]string{"agy": "antigravity"}

var (
	runtimeRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	// modelRE is the only shape of model id that may reach the runner's
	// command line: Exec does not validate it, and a leading "-" would be read
	// as a flag. Real ids include brackets (opus[1m]) and slashes
	// (openrouter/…).
	modelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,127}$`)
)

// ModelInfo is one servable model.
type ModelInfo struct {
	ID        string // "<runtime>/<model>", the id clients use
	Runtime   string
	Model     string // the runtime's model id; "default" means no --model
	Label     string
	Class     Class
	Validated bool     // the roster has a recent passing validation
	Efforts   []string // reasoning effort levels the model accepts
	Alias     bool     // another name for an earlier entry: resolvable, not listed
}

// CatalogFuncs are the lookups the catalog is built from. Production wires
// them to monomind and the roster; tests replace them.
type CatalogFuncs struct {
	Scan   func(ctx context.Context) (*monomind.ScanResult, error)
	Models func(ctx context.Context, runtime, binary string) ([]monomind.RuntimeModel, error)
	Caps   func(ctx context.Context) (*monomind.CapabilitySet, error)
	Roster func(ctx context.Context) ([]agentroster.Result, error)
	// Logf receives the catalog's own notices (a failed refresh). nil drops them.
	Logf func(format string, args ...any)
}

// Catalog lists every model of the installed runtimes. The runtimes' own
// listings are fetched in parallel, cached for a TTL, and a load that is
// already running is shared by every caller that arrives meanwhile.
type Catalog struct {
	f   CatalogFuncs
	ttl time.Duration
	now func() time.Time

	mu     sync.Mutex
	cached []ModelInfo
	loaded bool // cached is a real result, possibly an empty list
	at     time.Time
	flight *catalogFlight
	// lastLists is each runtime's last listing that succeeded. A runtime
	// whose listing fails at a refresh keeps it, so one bad listing does not
	// turn a model clients use into a 404.
	lastLists map[string][]monomind.RuntimeModel
	// failing marks the runtimes whose last listing failed, so a streak is
	// logged once and not on every retry.
	failing map[string]bool
}

type catalogFlight struct {
	done   chan struct{}
	models []ModelInfo
	err    error
}

// NewCatalog returns a Catalog that reuses a load for ttl.
func NewCatalog(f CatalogFuncs, ttl time.Duration) *Catalog {
	return &Catalog{f: f, ttl: ttl, now: time.Now}
}

const (
	// loadTimeout bounds one load: a scan plus the slowest runtime listing.
	loadTimeout = 90 * time.Second
	// staleRetryAfter is how soon a failed refresh is tried again while the
	// previous list is served meanwhile.
	staleRetryAfter = 30 * time.Second
)

// listTimeout bounds one runtime's own model listing, so a slow or hung
// runtime costs its models and not the whole list. A variable so a test can
// shorten it.
var listTimeout = 12 * time.Second

var errLoadAborted = errors.New("the model list could not be loaded")

func (c *Catalog) logf(format string, args ...any) {
	if c.f.Logf != nil {
		c.f.Logf(format, args...)
	}
}

// Models returns every model, aliases included. Once a list has been loaded it
// is always served at once: an expired one is reloaded in the background, so a
// slow or hung scan costs no request its time. The first load is waited for,
// and shared by every caller that arrives meanwhile. When a reload fails the
// last good list stays, and the reload is tried again after staleRetryAfter.
func (c *Catalog) Models(ctx context.Context) ([]ModelInfo, error) {
	c.mu.Lock()
	if c.loaded {
		models := c.cached
		if c.now().Sub(c.at) >= c.ttl && c.flight == nil {
			fl := &catalogFlight{done: make(chan struct{}), err: errLoadAborted}
			c.flight = fl
			go c.refreshInBackground(fl, models)
		}
		c.mu.Unlock()
		return models, nil
	}
	if fl := c.flight; fl != nil {
		c.mu.Unlock()
		select {
		case <-fl.done:
			return fl.models, fl.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	fl := &catalogFlight{done: make(chan struct{}), err: errLoadAborted}
	c.flight = fl
	c.mu.Unlock()
	c.runLoad(ctx, fl, nil, false)
	return fl.models, fl.err
}

// refreshInBackground reloads an expired list. It runs in a goroutine of its
// own, where a panic would end the process, so it recovers.
func (c *Catalog) refreshInBackground(fl *catalogFlight, stale []ModelInfo) {
	defer func() {
		if r := recover(); r != nil {
			c.logf("refreshing the model list panicked, serving the previous one: %v", r)
			c.mu.Lock()
			c.retrySoonLocked() // like any failed refresh: not again on the very next request
			c.mu.Unlock()
		}
	}()
	c.runLoad(context.Background(), fl, stale, true)
}

// runLoad performs one load as the leader of fl. However it ends, even in a
// panic, the flight is over and its waiters wake: they must never wait on a
// leader that is gone.
func (c *Catalog) runLoad(ctx context.Context, fl *catalogFlight, stale []ModelInfo, hadStale bool) {
	defer func() {
		c.mu.Lock()
		c.flight = nil
		c.mu.Unlock()
		close(fl.done)
	}()

	// The load outlives the caller that started it: others wait on it too.
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
	defer cancel()
	models, degraded, err := c.load(lctx)
	retrySoon := degraded // a runtime's listing fell back: do not keep that for a whole TTL
	if err != nil && hadStale {
		c.logf("refreshing the model list failed, serving the previous one: %v", err)
		models, err, retrySoon = stale, nil, true
	}
	fl.models, fl.err = models, err
	if err == nil {
		c.mu.Lock()
		c.cached, c.loaded, c.at = models, true, c.now()
		if retrySoon {
			c.retrySoonLocked()
		}
		c.mu.Unlock()
	}
}

// retrySoonLocked makes the cached list expire staleRetryAfter from now instead
// of a whole TTL from when it was loaded: the last refresh did not give a good
// list. Callers hold c.mu.
func (c *Catalog) retrySoonLocked() {
	c.at = c.now()
	if c.ttl > staleRetryAfter {
		c.at = c.at.Add(-(c.ttl - staleRetryAfter))
	}
}

// load builds the list. degraded reports that some runtime's own listing
// failed, timed out or panicked and was replaced by its last good one (or, with
// none, by whatever came back).
func (c *Catalog) load(ctx context.Context) (models []ModelInfo, degraded bool, err error) {
	scan, err := c.f.Scan(ctx)
	if err != nil {
		return nil, false, err
	}
	var caps *monomind.CapabilitySet
	if c.f.Caps != nil {
		caps, _ = c.f.Caps(ctx)
	}
	installed := scan.Installed()
	roster := c.rosterResults(ctx)

	c.mu.Lock()
	previous := maps.Clone(c.lastLists)
	c.mu.Unlock()

	lists := make([][]monomind.RuntimeModel, len(installed))
	failed := make([]bool, len(installed))
	var wg sync.WaitGroup
	for i, e := range installed {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// These goroutines are not the caller's: a panic here would end the
			// process, so it costs this runtime its listing and nothing else.
			defer func() {
				if r := recover(); r != nil {
					c.logf("listing the models of %s panicked: %v", e.ID, r)
					failed[i], lists[i] = true, previous[e.ID]
				}
			}()
			bin := ""
			if e.Binary != nil {
				bin = *e.Binary
			}
			lctx, cancel := context.WithTimeout(ctx, listTimeout)
			defer cancel()
			models, err := c.f.Models(lctx, e.ID, bin)
			// monomind.ListModels answers a listing that ran out of time with its
			// built-in fallback and no error, so a timeout counts as a failure
			// like an error does: the fallback must not replace the live list.
			// The strict variant the gateway uses says the same of a listing that
			// failed (ErrBuiltinModels): its list stands in, and is not trusted.
			if err != nil || lctx.Err() != nil {
				failed[i] = true
				if last, ok := previous[e.ID]; ok {
					lists[i] = last
				} else if err == nil || errors.Is(err, monomind.ErrBuiltinModels) {
					lists[i] = models
				} // else the runtime keeps its default model only
				return
			}
			lists[i] = models
		}()
	}
	wg.Wait()
	good := map[string][]monomind.RuntimeModel{}
	var newlyFailing []string
	c.mu.Lock()
	if c.lastLists == nil {
		c.lastLists, c.failing = map[string][]monomind.RuntimeModel{}, map[string]bool{}
	}
	for i, e := range installed {
		if failed[i] {
			degraded = true
			if !c.failing[e.ID] {
				c.failing[e.ID] = true
				newlyFailing = append(newlyFailing, e.ID)
			}
		} else {
			good[e.ID] = lists[i]
			delete(c.failing, e.ID)
		}
	}
	maps.Copy(c.lastLists, good)
	c.mu.Unlock()
	for _, id := range newlyFailing {
		c.logf("the model list of %s could not be refreshed: using the last one, and trying again soon", id)
	}

	validated := validatedIDs(roster, scan, c.now())
	var out []ModelInfo
	for i, e := range installed {
		class := ClassifyRuntime(e, caps)
		seen := map[string]bool{}
		add := func(m monomind.RuntimeModel) {
			if seen[m.ID] || !modelRE.MatchString(m.ID) {
				return
			}
			seen[m.ID] = true
			label := m.Label
			if label == "" {
				label = m.ID
			}
			id := e.ID + "/" + m.ID
			out = append(out, ModelInfo{
				ID: id, Runtime: e.ID, Model: m.ID, Label: label, Class: class,
				Validated: validated[id], Efforts: m.EffortLevels, Alias: m.AliasOf != "",
			})
		}
		listsDefault := false
		for _, m := range lists[i] {
			if m.ID == agentroster.DefaultModel {
				listsDefault = true
			}
		}
		if !listsDefault {
			add(monomind.RuntimeModel{ID: agentroster.DefaultModel, Label: e.ID + " default model"})
		}
		for _, m := range lists[i] {
			add(m)
		}
		// A model the user added to the roster by hand stays usable even when
		// the runtime does not list it.
		for _, r := range roster {
			if r.Runtime == e.ID {
				add(monomind.RuntimeModel{ID: r.Model, Label: r.Label, EffortLevels: r.EffortLevels})
			}
		}
	}
	return out, degraded, nil
}

// rosterResults returns the stored roster rows. The roster is machine-wide
// and may be empty or unreadable, which is not an error here.
func (c *Catalog) rosterResults(ctx context.Context) []agentroster.Result {
	if c.f.Roster == nil {
		return nil
	}
	results, err := c.f.Roster(ctx)
	if err != nil {
		return nil
	}
	return results
}

// validatedIDs returns the "<runtime>/<model>" ids with a recent passing
// validation.
func validatedIDs(results []agentroster.Result, scan *monomind.ScanResult, now time.Time) map[string]bool {
	ready := map[string]bool{}
	for _, rr := range agentroster.Build(results, scan, now, agentroster.DefaultMaxAge) {
		for _, m := range rr.Models {
			if m.State == agentroster.StateReady {
				ready[rr.Runtime+"/"+m.Model] = true
			}
		}
	}
	return ready
}

// splitModel parses a client's model name: "<runtime>/<model>", or a bare
// runtime meaning its default model. The runtime is case-insensitive and
// "agy" is accepted for antigravity. Both halves must have the shapes that
// may reach a command line.
func splitModel(name string) (runtime, model string, ok bool) {
	rt, model, found := strings.Cut(name, "/")
	rt = strings.ToLower(rt)
	if alias, isAlias := aliases[rt]; isAlias {
		rt = alias
	}
	if !found {
		model = agentroster.DefaultModel
	}
	if !runtimeRE.MatchString(rt) || !modelRE.MatchString(model) {
		return "", "", false
	}
	return rt, model, true
}

// Resolve finds the catalog entry a client's model name refers to.
func (c *Catalog) Resolve(ctx context.Context, name string) (ModelInfo, error) {
	rt, model, ok := splitModel(name)
	if !ok {
		return ModelInfo{}, ErrUnknownModel
	}
	models, err := c.Models(ctx)
	if err != nil {
		return ModelInfo{}, fmt.Errorf("listing models: %w", err)
	}
	for _, m := range models {
		if m.Runtime == rt && m.Model == model {
			return m, nil
		}
	}
	return ModelInfo{}, ErrUnknownModel
}

// Visible is the list a client may see: aliases hidden, and only the models
// the policy allows.
func (c *Catalog) Visible(ctx context.Context, p Policy) ([]ModelInfo, error) {
	models, err := c.Models(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		if !m.Alias && p.Allows(m.Class) {
			out = append(out, m)
		}
	}
	return out, nil
}
