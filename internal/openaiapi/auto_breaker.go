package openaiapi

import (
	"sync"
	"time"
)

// A profile's questions to Jev stop for autoBreakerCooldown after
// autoBreakerFailures of them in a row got no answer: while Jev is down every
// question would wait out the whole budget, with its request's slot held, to end
// in the rule's pick anyway.
const (
	autoBreakerFailures = 3
	autoBreakerCooldown = 30 * time.Second
)

// breakerTransition is what a question's outcome did to a breaker.
type breakerTransition int

const (
	breakerNone     breakerTransition = iota
	breakerOpened                     // the failure that stopped the questions
	breakerReopened                   // another failure while they were stopped: the probe's, or a question that was already out
	breakerClosed                     // an answer after they were stopped: Jev is back
)

// autoBreaker decides whether a profile's next question goes out. Closed, every
// question does. After threshold failures in a row it opens for cooldown: none
// does, and the rule picks. Then exactly one question goes out as the probe, while
// the others still use the rule: its answer closes the breaker, its failure opens
// it for another cooldown. Only a question that got no answer (an error, a
// timeout) is a failure: Jev answering something the rule has to override is Jev
// being up, and a caller that left says nothing.
type autoBreaker struct {
	threshold int
	cooldown  time.Duration
	now       func() time.Time

	mu       sync.Mutex
	failures int       // questions in a row that got no answer
	until    time.Time // the breaker is open while now is before it
	probing  bool      // the probe is out
}

func newAutoBreaker(threshold int, cooldown time.Duration, now func() time.Time) *autoBreaker {
	return &autoBreaker{threshold: threshold, cooldown: cooldown, now: now}
}

// allow reports whether to ask Jev. A true after the cooldown makes this question
// the probe: it must be followed by record or abandon.
func (b *autoBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < b.threshold {
		return true
	}
	if b.probing || b.now().Before(b.until) {
		return false
	}
	b.probing = true
	return true
}

// record is the outcome of a question that allow let through: whether Jev
// answered it.
func (b *autoBreaker) record(answered bool) breakerTransition {
	b.mu.Lock()
	defer b.mu.Unlock()
	wasOpen := b.failures >= b.threshold
	b.probing = false
	if answered {
		b.failures = 0
		if wasOpen {
			return breakerClosed
		}
		return breakerNone
	}
	b.failures++
	if b.failures < b.threshold {
		return breakerNone
	}
	b.until = b.now().Add(b.cooldown)
	if wasOpen {
		return breakerReopened
	}
	return breakerOpened
}

// abandon ends a question that says nothing about Jev, because its caller left:
// it counts as neither, and frees the probe if it was the probe.
func (b *autoBreaker) abandon() {
	b.mu.Lock()
	b.probing = false
	b.mu.Unlock()
}

// autoBreakerFor is the breaker of a profile's questions. Profiles do not share
// one: a revoked key is one profile's failure, not Jev's.
func (g *Gateway) autoBreakerFor(profileID string) *autoBreaker {
	g.breakerMu.Lock()
	defer g.breakerMu.Unlock()
	b, ok := g.breakers[profileID]
	if !ok {
		b = newAutoBreaker(autoBreakerFailures, autoBreakerCooldown, g.now)
		g.breakers[profileID] = b
	}
	return b
}
