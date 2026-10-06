package account

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// These tests are in package account because adopt, cached and Close's fields
// are what the refresh code and the refresher build on, and they are not
// exported. They cannot use accounttest, which imports account: the one token
// they need is minted by mintToken (monotonic_internal_test.go).

// stepClock is a settable clock, safe for concurrent use.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStepClock() *stepClock {
	return &stepClock{now: time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)}
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestAdoptReplacesTheCachedSessionAndItsVerification(t *testing.T) {
	clock := newStepClock()
	SetEnforceFromForTest(t, clock.Now().Add(-24*time.Hour))
	token := mintToken(t, clock.Now().Add(-10*time.Minute))
	store := OpenStore(filepath.Join(t.TempDir(), "account"), NewMemorySealer())
	// On disk the login was refused, so a guard that read the file would say so.
	if err := store.Save(&Session{V: 1, Host: HostURL, User: &User{ID: "u-1"}, State: stateRefused}); err != nil {
		t.Fatal(err)
	}
	g := NewGuard(GuardOptions{Store: store, Now: clock.Now})
	t.Cleanup(g.Close)

	adopted := &Session{V: 1, Host: HostURL, AccessToken: token, User: &User{ID: "u-1"}, HW: clock.Now().Add(-10 * time.Minute)}
	g.adopt(adopted)
	if st := g.Status(); st.State != StateOK {
		t.Fatalf("right after adopt: Status = %s/%q, want ok: the adopted session answers before anything is read", st.State, st.Reason)
	}
	if sess, rcpt := g.cached(); sess != adopted || rcpt == nil || rcpt.Sub != "u-1" {
		t.Fatalf("cached() returned the adopted session: %t, a receipt: %t; want both, with the token's subject", sess == adopted, rcpt != nil)
	}
	// adopt recorded the modification time of the file it found, so a poll that
	// finds the file as it was keeps the adopted session.
	clock.Advance(PollInterval)
	if st := g.Status(); st.State != StateOK {
		t.Fatalf("a poll that found the file unchanged: Status = %s/%q, want the adopted session", st.State, st.Reason)
	}

	// A refused session has no receipt, even with a token still in it.
	refused := &Session{V: 1, Host: HostURL, AccessToken: token, User: &User{ID: "u-1"}, State: stateRefused}
	g.adopt(refused)
	if sess, rcpt := g.cached(); sess != refused || rcpt != nil {
		t.Fatalf("cached() returned the refused session: %t, and a receipt: %t; want the session and no receipt", sess == refused, rcpt != nil)
	}
	if st := g.Status(); st.State != StateLocked || st.Reason != ReasonRefused {
		t.Fatalf("Status = %s/%q, want locked/refused", st.State, st.Reason)
	}

	// The session is gone.
	g.adopt(nil)
	if sess, rcpt := g.cached(); sess != nil || rcpt != nil {
		t.Fatal("cached() returned something after adopt(nil)")
	}
	if st := g.Status(); st.State != StateLocked || st.Reason != ReasonNotLoggedIn {
		t.Fatalf("Status = %s/%q, want locked/not_logged_in", st.State, st.Reason)
	}
}

// What the guard holds from a read that failed ends with the session the caller
// has just written, or found gone.
func TestAdoptEndsTheMemoryOfAFailedRead(t *testing.T) {
	clock := newStepClock()
	dir := filepath.Join(t.TempDir(), "account")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := NewGuard(GuardOptions{Store: OpenStore(dir, NewMemorySealer()), Now: clock.Now})
	t.Cleanup(g.Close)
	if st := g.Status(); st.Reason != ReasonInvalid {
		t.Fatalf("Status = %s/%q, want locked/invalid", st.State, st.Reason)
	}
	g.adopt(nil)
	if st := g.Status(); st.State != StateLocked || st.Reason != ReasonNotLoggedIn {
		t.Fatalf("Status = %s/%q after adopt(nil), want locked/not_logged_in", st.State, st.Reason)
	}
}

func TestCloseCancelsTheRefresherAndWaitsForItsEnd(t *testing.T) {
	g := NewGuard(GuardOptions{Store: OpenStore(filepath.Join(t.TempDir(), "account"), NewMemorySealer())})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exit := make(chan struct{})
	done := make(chan struct{})
	go func() { // a refresher that takes a moment to wind down
		<-ctx.Done()
		<-exit
		close(done)
	}()
	g.mu.Lock()
	g.loopCancel, g.loopDone = cancel, done
	g.mu.Unlock()

	returned := make(chan struct{})
	go func() {
		g.Close()
		close(returned)
	}()
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not stop the refresher")
	}
	// Close must still be waiting for the refresher. A negative wait of 150 ms,
	// above the 100 ms floor for a test that waits on real time.
	select {
	case <-returned:
		t.Fatal("Close returned while the refresher was still winding down")
	case <-time.After(150 * time.Millisecond):
	}
	close(exit)
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return once the refresher had ended")
	}
	g.mu.Lock()
	closed := g.closed
	g.mu.Unlock()
	if !closed {
		t.Fatal("Close did not mark the guard closed: a refresher started afterwards would never be stopped")
	}
	g.Close() // twice is fine
}

// adopt, cached and Close are called from the refresh code while Status runs in
// other goroutines: each takes the guard's lock, as the start of a refresher
// does. Run with -race.
func TestTheGuardsInternalsTakeItsLock(t *testing.T) {
	clock := newStepClock()
	SetEnforceFromForTest(t, clock.Now().Add(-24*time.Hour))
	token := mintToken(t, clock.Now().Add(-10*time.Minute))
	g := NewGuard(GuardOptions{Store: OpenStore(filepath.Join(t.TempDir(), "account"), NewMemorySealer()), Now: clock.Now})
	t.Cleanup(g.Close)
	sessions := []*Session{
		{V: 1, Host: HostURL, AccessToken: token, User: &User{ID: "u-1"}, HW: clock.Now().Add(-10 * time.Minute)},
		{V: 1, Host: HostURL, User: &User{ID: "u-1"}, State: stateRefused},
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				g.Status()
				g.cached()
			}
		}()
	}
	for i := range 200 {
		g.adopt(sessions[i%2])
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	started := make(chan struct{})
	go func() { // the start of a refresher: under the lock, unless the guard is closed
		g.mu.Lock()
		if !g.closed {
			g.loopCancel, g.loopDone = cancel, done
			go func() {
				<-ctx.Done()
				close(done)
			}()
		}
		g.mu.Unlock()
		close(started)
	}()
	g.Close()
	<-started
	close(stop)
	wg.Wait()
}
