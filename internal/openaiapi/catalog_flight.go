package openaiapi

import "context"

// How a list of models is loaded, shared by the callers that arrive meanwhile,
// reused for the TTL and refreshed in the background: the part of the catalog that
// is about when a load runs. What a load lists is catalog.go's.

// Models returns every model, aliases included. Once a list has been loaded it
// is always served at once: an expired one is reloaded in the background, so a
// slow or hung scan costs no request its time. The first load is waited for,
// and shared by every caller that arrives meanwhile. When a reload fails the
// last good list stays, and the reload is tried again after staleRetryAfter.
func (c *Catalog) Models(ctx context.Context) ([]ModelInfo, error) {
	return c.models(ctx, false)
}

// ModelsBound is Models for a caller whose own lifetime is the load's: a one-shot
// command, or the tools of a server that stops all its callers together, not a
// gateway whose first request must not decide for everyone who waits behind it.
// The first load, which Models detaches from its caller so that others can wait on
// it, runs under ctx here: cancelling ctx stops the processes it started, and the
// call returns at once instead of waiting for them to close their pipes. A load
// that ctx cut short is not cached. A caller that joins a load someone else
// started waits for it as in Models, and starts one of its own if that starter
// left while it did not. A list that is already cached is served as in Models.
func (c *Catalog) ModelsBound(ctx context.Context) ([]ModelInfo, error) {
	return c.models(ctx, true)
}

func (c *Catalog) models(ctx context.Context, bound bool) ([]ModelInfo, error) {
	for {
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
				if fl.abandoned && ctx.Err() == nil {
					continue // its starter left and this caller did not: it loads for itself
				}
				return fl.models, fl.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		fl := &catalogFlight{done: make(chan struct{}), err: errLoadAborted}
		c.flight = fl
		c.mu.Unlock()
		if !bound {
			c.runLoad(ctx, fl, nil, false, false)
			return fl.models, fl.err
		}
		go c.runBound(ctx, fl)
		select {
		case <-fl.done:
			return fl.models, fl.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// runBound runs a bound caller's load on a goroutine of its own, so that the
// caller can stop waiting for it. Nobody above it would recover a panic there.
func (c *Catalog) runBound(ctx context.Context, fl *catalogFlight) {
	defer func() {
		if r := recover(); r != nil {
			c.logf("loading the model list panicked: %v", r)
		}
	}()
	c.runLoad(ctx, fl, nil, false, true)
}

// refreshInBackground reloads an expired list. It runs in a goroutine of its
// own, where a panic would end the process, so it recovers.
func (c *Catalog) refreshInBackground(fl *catalogFlight, stale []ModelInfo) {
	defer func() {
		if r := recover(); r != nil {
			c.logf("refreshing the model list panicked, serving the previous one: %v", r)
			c.mu.Lock()
			c.failStreak++
			c.retrySoonLocked() // like any failed refresh: not again on the very next request
			c.mu.Unlock()
		}
	}()
	c.runLoad(context.Background(), fl, stale, true, false)
}

// runLoad performs one load as the leader of fl. However it ends, even in a
// panic, the flight is over and its waiters wake: they must never wait on a
// leader that is gone.
func (c *Catalog) runLoad(ctx context.Context, fl *catalogFlight, stale []ModelInfo, hadStale, bound bool) {
	defer func() {
		c.mu.Lock()
		c.flight = nil
		c.mu.Unlock()
		close(fl.done)
	}()

	// The load outlives the caller that started it: others wait on it too. A bound
	// caller's load is its own, and ends with it.
	base := context.WithoutCancel(ctx)
	if bound {
		base = ctx
	}
	lctx, cancel := context.WithTimeout(base, loadTimeout)
	defer cancel()
	models, degraded, err := c.load(lctx)
	if bound && ctx.Err() != nil {
		// The starter left while the load ran, so what it holds may be cut short: a
		// listing that the cancellation failed is replaced by its runtime's default
		// model, and the load still counts as a success. It is neither cached nor
		// handed to the callers that joined it.
		fl.abandoned, fl.err = true, ctx.Err()
		return
	}
	retrySoon := degraded // a runtime's listing fell back: do not keep that for a whole TTL
	if err != nil && hadStale {
		c.logf("refreshing the model list failed, serving the previous one: %v", err)
		models, err, retrySoon = stale, nil, true
	}
	fl.models, fl.err = models, err
	if err == nil {
		c.mu.Lock()
		first := !c.loaded
		c.cached, c.loaded, c.at = models, true, c.now()
		if retrySoon {
			c.failStreak++
			c.retrySoonLocked()
		} else {
			c.failStreak = 0
		}
		c.mu.Unlock()
		if first && c.onFirstLoad != nil {
			c.onFirstLoad(models)
		}
	}
}

// retrySoonLocked makes the cached list expire sooner than a whole TTL after it
// was loaded, because the last refresh did not give a good list: after
// staleRetryAfter, and after each further failure in a row twice as long as the
// time before, up to the TTL. A listing that keeps failing (a runtime that is
// installed but not signed in) must not make every half minute of traffic reload
// the whole catalog. Callers hold c.mu.
func (c *Catalog) retrySoonLocked() {
	delay := staleRetryAfter
	for i := 1; i < c.failStreak && delay < c.ttl; i++ {
		delay *= 2
	}
	if delay > c.ttl {
		delay = c.ttl
	}
	c.at = c.now().Add(-(c.ttl - delay))
}
