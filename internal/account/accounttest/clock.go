// Package accounttest holds the fixtures every test of the monoes.me account
// gate uses: a throwaway signing key and token minting (Fixture), a settable
// clock (Clock), a guard in a chosen state (Install) and the fixed development
// key pair (DevKeyPair). Only test binaries may use it: it calls the
// account.*ForTest hooks, which panic anywhere else.
package accounttest

import (
	"sync"
	"time"
)

// DefaultNow is where a fresh Clock starts: a fixed instant, so tests do not
// depend on the wall clock.
var DefaultNow = time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)

// Clock is a settable clock safe for concurrent use. Its Now method is what a
// guard takes as GuardOptions.Now.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock that reads t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t, forward or back.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

// Advance moves the clock by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
