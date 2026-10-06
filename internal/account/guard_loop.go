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
	// After a refresh, no attempt before holdUntil: half the new token's lifetime
	// after heldAt, the start of the pass that refreshed. monoes.me rotates the
	// refresh token on every use, and a local clock half a lifetime or more ahead
	// of monoes.me sees each token it has just been given as past its half-life,
	// so without the hold every pass would refresh again.
	var heldAt, holdUntil time.Time
	for ctx.Err() == nil {
		now := g.now()
		// A hold made after now means the clock went back, and ends it. A stored time
		// must never keep a refresh off, or a clock set back could not be repaired by
		// the refresh that resets it (see elapsed).
		held := holdUntil.After(now) && !heldAt.After(now)
		// A notBefore further away than the longest backoff means the clock went back.
		if !held && (!notBefore.After(now) || notBefore.Sub(now) > backoffMax) {
			_, oc, _ := g.refreshIfDue(ctx, modeBackground)
			switch oc {
			case outcomeFailed:
				backoff = min(max(backoff*2, backoffMin), backoffMax)
				notBefore = now.Add(backoff)
			case outcomeRefreshed:
				backoff, notBefore = 0, time.Time{}
				_, rcpt := g.cached()
				heldAt, holdUntil = now, now.Add(holdFor(rcpt))
			case outcomeRefused:
				backoff, notBefore = 0, time.Time{}
			}
		} else {
			g.Status()     // still follows the file and fires OnRefused
			g.touchHW(now) // and, as a pass that finds nothing due does, keeps the high-water mark current
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

// holdFor is how long the refresher leaves alone a token it has just obtained:
// half its lifetime, the half-life that makes a token due. With no receipt (the
// cache could not read the token back) it is the shortest backoff.
func holdFor(rcpt *Receipt) time.Duration {
	if rcpt == nil {
		return backoffMin
	}
	return rcpt.ExpiresAt.Sub(rcpt.IssuedAt) / 2
}
