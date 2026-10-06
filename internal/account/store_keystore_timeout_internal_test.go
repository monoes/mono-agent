package account

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The key store is read (Open) and written (Seal) while the machine-wide
// session.lock is held, the write after monoes.me has rotated the refresh token,
// and the OS key stores wait without bound for a locked keychain or an unlock
// prompt nobody answers. Unbounded, one waiting key store holds the lock against
// every process of the machine, so the store bounds each call by keyStoreTimeout
// and reports an unanswered one as ErrKeyringUnavailable. These tests are in
// package account because keyStoreTimeout, the seam that shortens the wait, is
// not exported.

// gatedSealer is a Sealer whose calls wait until the test lets them go, as the
// key store of a locked keychain does while an unlock prompt waits for an answer.
// A nil gate lets that kind of call straight through to the inner sealer.
type gatedSealer struct {
	inner    Sealer
	holdOpen chan struct{}
	holdSeal chan struct{}
	once     sync.Once
	began    atomic.Int32 // calls that have entered the sealer
	ended    atomic.Int32 // calls that have left it
}

func (s *gatedSealer) Open(sealed []byte) ([]byte, error) {
	s.began.Add(1)
	defer s.ended.Add(1)
	if s.holdOpen != nil {
		<-s.holdOpen
	}
	return s.inner.Open(sealed)
}

func (s *gatedSealer) Seal(plain []byte) ([]byte, error) {
	s.began.Add(1)
	defer s.ended.Add(1)
	if s.holdSeal != nil {
		<-s.holdSeal
	}
	return s.inner.Seal(plain)
}

// release lets every held call go, once; it is safe to call twice.
func (s *gatedSealer) release() {
	s.once.Do(func() {
		if s.holdOpen != nil {
			close(s.holdOpen)
		}
		if s.holdSeal != nil {
			close(s.holdSeal)
		}
	})
}

// setKeyStoreTimeout shortens the wait for the rest of the test. A test that
// uses it must not call t.Parallel().
func setKeyStoreTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := keyStoreTimeout
	keyStoreTimeout = d
	t.Cleanup(func() { keyStoreTimeout = prev })
}

// timed runs fn on its own goroutine and fails the test unless it returns within
// 5 s: a key store that is waited for forever is the very failure these tests
// look for, and it must show as a failed assertion, not as a test that hangs. It
// returns how long fn took.
func timed(t *testing.T, what string, fn func()) time.Duration {
	t.Helper()
	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return time.Since(start)
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return within 5 s while the key store waits", what)
		return 0
	}
}

// goroutinesIn counts the goroutines whose stack mentions name.
func goroutinesIn(name string) int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, stack := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(stack, name) {
			count++
		}
	}
	return count
}

// waitUntil polls cond for up to 3 s.
func waitUntil(cond func() bool) bool {
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// storeWithToken is a store holding refresh token rt, sealed by inner, and the
// directory it lives in.
func storeWithToken(t *testing.T, inner Sealer, rt string) (Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "account")
	if err := OpenStore(dir, inner).SaveRefresh(rt); err != nil {
		t.Fatal(err)
	}
	return OpenStore(dir, inner), dir
}

func TestTheKeyStoreTimeoutIsTenSeconds(t *testing.T) {
	if keyStoreTimeout != 10*time.Second {
		t.Fatalf("keyStoreTimeout = %v, want 10 s: long enough for a keychain prompt, short enough that a refresh does not hold session.lock for a minute", keyStoreTimeout)
	}
}

func TestLoadRefreshGivesUpOnAKeyStoreThatDoesNotAnswer(t *testing.T) {
	setKeyStoreTimeout(t, 200*time.Millisecond)
	inner := NewMemorySealer()
	_, dir := storeWithToken(t, inner, "rt-1")
	waiting := &gatedSealer{inner: inner, holdOpen: make(chan struct{})}
	t.Cleanup(waiting.release)
	st := OpenStore(dir, waiting)

	var token string
	var err error
	took := timed(t, "LoadRefresh", func() { token, err = st.LoadRefresh() })
	if token != "" || !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("LoadRefresh = %q, %v, want no token and an error that is ErrKeyringUnavailable", token, err)
	}
	if want := "did not answer within 200ms"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not say %q", err, want)
	}
	if took < keyStoreTimeout {
		t.Fatalf("LoadRefresh returned after %v, before the %v timeout", took, keyStoreTimeout)
	}
	// Once the key store answers, the call that was given up on ends cleanly.
	waiting.release()
	if !waitUntil(func() bool { return waiting.ended.Load() == 1 && goroutinesIn("callKeyStore") == 0 }) {
		t.Fatalf("the abandoned call did not end after the key store answered (%d calls ended, %d goroutines left)", waiting.ended.Load(), goroutinesIn("callKeyStore"))
	}
}

func TestSaveRefreshGivesUpOnAKeyStoreThatDoesNotAnswerAndWritesNothing(t *testing.T) {
	setKeyStoreTimeout(t, 200*time.Millisecond)
	inner := NewMemorySealer()
	_, dir := storeWithToken(t, inner, "rt-1") // the token that is about to be rotated away
	waiting := &gatedSealer{inner: inner, holdSeal: make(chan struct{})}
	t.Cleanup(waiting.release)
	st := OpenStore(dir, waiting)
	before := snapshotDir(t, dir)

	var err error
	took := timed(t, "SaveRefresh", func() { err = st.SaveRefresh("rt-2") })
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("SaveRefresh = %v, want an error that is ErrKeyringUnavailable", err)
	}
	if want := "did not answer within 200ms"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not say %q", err, want)
	}
	if took < keyStoreTimeout {
		t.Fatalf("SaveRefresh returned after %v, before the %v timeout", took, keyStoreTimeout)
	}
	if after := snapshotDir(t, dir); after != before {
		t.Fatalf("a SaveRefresh that timed out changed the directory: %s, was %s", after, before)
	}
	// The abandoned Seal must never write: its result is dropped, and it is the
	// caller that writes refresh.enc, and only a result that arrived in time.
	waiting.release()
	if !waitUntil(func() bool { return waiting.ended.Load() == 1 && goroutinesIn("callKeyStore") == 0 }) {
		t.Fatalf("the abandoned call did not end after the key store answered (%d calls ended, %d goroutines left)", waiting.ended.Load(), goroutinesIn("callKeyStore"))
	}
	if after := snapshotDir(t, dir); after != before {
		t.Fatalf("the Seal that was given up on wrote after the key store answered: %s, was %s", after, before)
	}
	if rt, err := OpenStore(dir, inner).LoadRefresh(); err != nil || rt != "rt-1" {
		t.Fatalf("refresh.enc holds %q (%v), want the token it held", rt, err)
	}
}

func TestSaveRefreshThatTimesOutLeavesNoFileWhereThereWasNone(t *testing.T) {
	setKeyStoreTimeout(t, 100*time.Millisecond)
	waiting := &gatedSealer{inner: NewMemorySealer(), holdSeal: make(chan struct{})}
	t.Cleanup(waiting.release)
	dir := filepath.Join(t.TempDir(), "account")
	st := OpenStore(dir, waiting)
	var err error
	timed(t, "SaveRefresh", func() { err = st.SaveRefresh("rt-1") })
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("SaveRefresh = %v, want an error that is ErrKeyringUnavailable", err)
	}
	waiting.release()
	if !waitUntil(func() bool { return waiting.ended.Load() == 1 }) {
		t.Fatal("the abandoned Seal did not end")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a SaveRefresh that timed out created %s (stat err %v)", dir, err)
	}
}

// snapshotDir describes every file of dir by name and content, so that a test
// can show that nothing in it changed.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s=%x", e.Name(), data))
	}
	return strings.Join(out, ";")
}

// A call that the key store answers is not slowed and not changed: it returns
// what the sealer returned, as soon as it returned it, and the sealer's own
// errors come through as they are.
func TestAKeyStoreThatAnswersIsNotSlowedOrChanged(t *testing.T) {
	inner := NewMemorySealer() // the default timeout: nobody waits ten seconds for this
	dir := filepath.Join(t.TempDir(), "account")
	st := OpenStore(dir, inner)
	var err error
	if took := timed(t, "SaveRefresh", func() { err = st.SaveRefresh("rt-1") }); err != nil || took > 4*time.Second {
		t.Fatalf("SaveRefresh = %v after %v, want no error at once", err, took)
	}
	var rt string
	if took := timed(t, "LoadRefresh", func() { rt, err = st.LoadRefresh() }); err != nil || rt != "rt-1" || took > 4*time.Second {
		t.Fatalf("LoadRefresh = %q, %v after %v, want rt-1 at once", rt, err, took)
	}

	boom := fmt.Errorf("%w: the stored key does not open the sealed refresh token", ErrKeyringUnavailable)
	refusing := OpenStore(dir, errSealer{boom})
	if _, got := refusing.LoadRefresh(); got != boom {
		t.Fatalf("LoadRefresh with a sealer that fails = %v, want the sealer's own error", got)
	}
	if got := refusing.SaveRefresh("rt-2"); got != boom {
		t.Fatalf("SaveRefresh with a sealer that fails = %v, want the sealer's own error", got)
	}
	plain := errors.New("the sealer's own error, of no known type")
	if _, got := OpenStore(dir, errSealer{plain}).LoadRefresh(); got != plain {
		t.Fatalf("LoadRefresh = %v, want an error of another type passed through unchanged", got)
	}
}

// errSealer fails every call with err.
type errSealer struct{ err error }

func (s errSealer) Seal([]byte) ([]byte, error) { return nil, s.err }
func (s errSealer) Open([]byte) ([]byte, error) { return nil, s.err }

// A call that was given up on keeps running until the key store answers, and then
// it must end: with nobody waiting for its result, it still must not block on
// handing it over. A goroutine per unanswered call that never ends would leak
// for as long as the process runs.
func TestAbandonedKeyStoreCallsEndOnceTheKeyStoreAnswers(t *testing.T) {
	setKeyStoreTimeout(t, 20*time.Millisecond)
	inner := NewMemorySealer()
	_, dir := storeWithToken(t, inner, "rt-1")
	waiting := &gatedSealer{inner: inner, holdOpen: make(chan struct{}), holdSeal: make(chan struct{})}
	t.Cleanup(waiting.release)
	st := OpenStore(dir, waiting)

	const rounds = 6
	for range rounds {
		var loadErr, saveErr error
		timed(t, "LoadRefresh", func() { _, loadErr = st.LoadRefresh() })
		timed(t, "SaveRefresh", func() { saveErr = st.SaveRefresh("rt-2") })
		if !errors.Is(loadErr, ErrKeyringUnavailable) || !errors.Is(saveErr, ErrKeyringUnavailable) {
			t.Fatalf("LoadRefresh = %v, SaveRefresh = %v, want ErrKeyringUnavailable from both", loadErr, saveErr)
		}
	}
	// Control: the abandoned calls are alive, and this test sees them.
	if got := goroutinesIn("callKeyStore"); got != 2*rounds {
		t.Fatalf("%d goroutines in the key store call while it waits, want %d: this test cannot tell a leak from none", got, 2*rounds)
	}
	waiting.release()
	if !waitUntil(func() bool { return goroutinesIn("callKeyStore") == 0 }) {
		t.Fatalf("%d of %d abandoned calls are still alive 3 s after the key store answered: they cannot hand over a result nobody waits for", goroutinesIn("callKeyStore"), 2*rounds)
	}
}

// A refresh whose key store never answers lets go of session.lock when the
// timeout ends, so that the other processes' refreshes go through while it is
// still waiting. Without the bound one waiting key store held the lock against
// the whole machine: every other due command waited for it 25 s and recorded
// nothing.
func TestAKeyStoreThatWaitsForeverDoesNotHoldTheSessionLockPastItsTimeout(t *testing.T) {
	r := newRig(t)
	r.signIn(2 * time.Hour) // in grace: due for every process
	setKeyStoreTimeout(t, 300*time.Millisecond)
	waiting := &gatedSealer{inner: r.seal, holdOpen: make(chan struct{})}
	t.Cleanup(waiting.release)
	a := NewGuard(GuardOptions{Store: OpenStore(r.dir, waiting), Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(a.Close)
	b := NewGuard(GuardOptions{Store: OpenStore(r.dir, r.seal), Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(b.Close)

	// Process A is due, takes the lock and reads refresh.enc: the key store waits.
	type answer struct {
		st  Status
		err error
	}
	answers := func(g *Guard) chan answer {
		ch := make(chan answer, 1)
		go func() {
			st, err := g.EnsureFresh(context.Background())
			ch <- answer{st, err}
		}()
		return ch
	}
	a1 := answers(a)
	if !waitUntil(func() bool { return waiting.began.Load() == 1 }) {
		t.Fatal("process A never reached the key store")
	}

	// Process B, an ordinary command a minute later (past the negative cache that
	// A's failed attempt sets), is due and waits for the lock A holds.
	r.clock.Advance(time.Minute)
	b1 := answers(b)

	// The timeout lets A go; the key store is still waiting. B then gets the lock
	// and its refresh goes through.
	select {
	case got := <-a1:
		if got.err != nil || got.st.State != StateGrace || got.st.Reason != ReasonKeyringUnavailable {
			t.Fatalf("process A = %s/%q, %v, want grace/keyring_unavailable: it had no token to send", got.st.State, got.st.Reason, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process A did not return within 5 s while the key store waits")
	}
	select {
	case got := <-b1:
		if got.err != nil || got.st.State != StateOK || r.srv.calls.Load() != 1 {
			t.Fatalf("process B = %s/%q, %v with %d network refreshes, want ok after one", got.st.State, got.st.Reason, got.err, r.srv.calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process B did not return within 5 s: the lock was held while the key store waits")
	}
	if waiting.ended.Load() != 0 {
		t.Fatal("the key store call has ended: the lock was let go by its answer, not by the timeout")
	}
}

// The write of the new refresh token is the second key store call of a refresh,
// made after monoes.me has rotated the old one. When it times out the dead token
// is removed from disk (os.Remove needs no key store), so that no later process
// presents it, and the attempt is recorded as a key store failure: the session
// keeps its grace, and the account is not revoked.
func TestASaveRefreshThatTimesOutAfterTheGrantRemovesTheDeadTokenAndRecordsTheFailure(t *testing.T) {
	r := newRig(t)
	r.signIn(2 * time.Hour) // due
	setKeyStoreTimeout(t, 300*time.Millisecond)
	waiting := &gatedSealer{inner: r.seal, holdSeal: make(chan struct{})}
	t.Cleanup(waiting.release)
	a := NewGuard(GuardOptions{Store: OpenStore(r.dir, waiting), Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(a.Close)

	var st Status
	var err error
	timed(t, "EnsureFresh", func() { st, err = a.EnsureFresh(context.Background()) })
	if r.srv.calls.Load() != 1 {
		t.Fatalf("%d network refreshes, want the one whose answer could not be stored", r.srv.calls.Load())
	}
	if !errors.Is(err, ErrKeyringUnavailable) || st.State != StateGrace || st.Reason != ReasonKeyringUnavailable {
		t.Fatalf("EnsureFresh = %s/%q, %v, want grace/keyring_unavailable and an error that is ErrKeyringUnavailable", st.State, st.Reason, err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, refreshFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the dead refresh token is still on disk (stat err %v)", err)
	}
	if sess, err := r.store.Load(); err != nil || sess.LastResult != string(ReasonKeyringUnavailable) {
		t.Fatalf("stored session = %+v (%v), want the attempt recorded as keyring_unavailable", sess, err)
	}
	// No later attempt, by this process or by another, presents the dead token.
	b := NewGuard(GuardOptions{Store: OpenStore(r.dir, r.seal), Refresher: r.srv, Now: r.clock.Now})
	t.Cleanup(b.Close)
	for range 3 {
		r.clock.Advance(2 * time.Minute)
		for _, g := range []*Guard{a, b} {
			if st, _ := g.EnsureFresh(context.Background()); st.State != StateGrace || st.Reason == ReasonRefused {
				t.Fatalf("Status = %s/%q, want grace and never refused", st.State, st.Reason)
			}
		}
	}
	if n := r.srv.calls.Load(); n != 1 {
		t.Fatalf("%d network refreshes: the dead refresh token was presented again", n)
	}
	// The Seal that was given up on writes nothing when the key store answers.
	waiting.release()
	if !waitUntil(func() bool { return waiting.ended.Load() == 1 }) {
		t.Fatal("the abandoned Seal did not end")
	}
	if _, err := os.Stat(filepath.Join(r.dir, refreshFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the Seal that was given up on wrote refresh.enc (stat err %v)", err)
	}
}
