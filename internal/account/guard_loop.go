package account

import (
	"context"
	"time"
)

// StartRefresher starts the background refresher: it follows session.json,
// tells OnRefused callbacks about a refusal, and refreshes at half the token's
// lifetime, retrying a failure with a backoff of 30 seconds doubling to 5
// minutes. It is idempotent, does nothing while the package is dormant (spec
// D22) and ends when ctx ends or the guard is closed. A daemon starts it at
// once; any other process starts it after LateRefresher.
func (g *Guard) StartRefresher(ctx context.Context) {
	if dormant() {
		return
	}
	g.mu.Lock()
	if g.closed || g.loopCancel != nil {
		g.mu.Unlock()
		return
	}
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	g.loopCancel, g.loopDone = cancel, done
	g.mu.Unlock()
	go func() {
		defer close(done)
		g.runLoop(lctx)
	}()
}

// runLoop wakes every poll and judges everything by the guard's clock, so a
// test that moves the clock controls it exactly. It keeps running with no
// session, so a sign-in from another process is picked up by itself.
func (g *Guard) runLoop(ctx context.Context) {
	ticker := time.NewTicker(g.poll)
	defer ticker.Stop()
	var backoff time.Duration
	var notBefore time.Time // no attempt before this, by the guard's clock; counted from the start of the failed attempt
	for ctx.Err() == nil {
		now := g.now()
		// A notBefore further away than the longest backoff means the clock went back.
		if !notBefore.After(now) || notBefore.Sub(now) > backoffMax {
			_, oc, _ := g.refreshIfDue(ctx, modeBackground)
			switch oc {
			case outcomeFailed:
				backoff = min(max(backoff*2, backoffMin), backoffMax)
				notBefore = now.Add(backoff)
			case outcomeRefreshed, outcomeRefused:
				backoff, notBefore = 0, time.Time{}
			}
		} else {
			g.Status() // still follows the file and fires OnRefused
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}
