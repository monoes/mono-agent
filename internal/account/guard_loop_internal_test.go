package account

import (
	"bytes"
	"context"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests are in package account because holdFor is not exported, and the
// case it covers best, a token that cannot be read back, cannot be made to
// happen from outside: NewSession has just verified the token that the cache
// verifies again. The race test counts goroutines by their stack, which only
// this package can name.

func TestHoldForIsHalfTheLifetimeOfTheTokenJustObtained(t *testing.T) {
	at := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	receipt := func(life time.Duration) *Receipt { return &Receipt{IssuedAt: at, ExpiresAt: at.Add(life)} }
	cases := []struct {
		name string
		rcpt *Receipt
		want time.Duration
	}{
		{"a token that lives an hour", receipt(time.Hour), 30 * time.Minute},
		{"the longest life a token may have", receipt(MaxTokenLife), MaxTokenLife / 2},
		{"a token that lives eight minutes", receipt(8 * time.Minute), 4 * time.Minute},
		{"an odd number of seconds", receipt(61 * time.Second), 30*time.Second + 500*time.Millisecond},
		{"no receipt: the cache cannot read the token it just stored", nil, backoffMin},
	}
	for _, c := range cases {
		if got := holdFor(c.rcpt); got != c.want {
			t.Errorf("%s: holdFor = %v, want %v", c.name, got, c.want)
		}
	}
}

// loopsRunning counts the goroutines that are inside runLoop.
func loopsRunning() int {
	var stacks bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)
	return strings.Count(stacks.String(), "(*Guard).runLoop(")
}

// StartRefresher checks closed and sets loopCancel and loopDone in one hold of
// mu. Otherwise a Close that comes between the two holds finds no loop to stop,
// and the loop that the caller then starts runs for good; two callers that both
// pass the check start two loops, and Close stops only the one it was told of.
// The race is a few nanoseconds wide, so the test runs it many times, with the
// callers let go at the same moment, and counts the loops that are left.
func TestStartRefresherCallsRacingWithEachOtherAndWithCloseLeaveNoLoopRunning(t *testing.T) {
	r := newRig(t)
	before := loopsRunning()
	for i := 0; i < 400; i++ {
		g := NewGuard(GuardOptions{Store: r.store, Now: r.clock.Now, Poll: time.Hour})
		start := make(chan struct{})
		var wg sync.WaitGroup
		for j := 0; j < 3; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				g.StartRefresher(context.Background())
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			g.Close()
		}()
		close(start)
		finished := make(chan struct{})
		go func() {
			wg.Wait()
			g.Close() // a Close that came first stopped nothing; this one stops what the callers started
			close(finished)
		}()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("StartRefresher and Close did not return: Close waits for a loop that does not end")
		}
	}
	time.Sleep(150 * time.Millisecond) // a loop that Close did not know of has started and waits for its tick
	if n := loopsRunning() - before; n != 0 {
		t.Fatalf("%d refresher loops are still running after every guard was closed", n)
	}
}
