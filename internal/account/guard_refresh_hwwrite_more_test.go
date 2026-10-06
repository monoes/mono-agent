package account_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// failingSaveStore is a Store whose Save can be made to fail, as a full disk or a
// directory that has gone read-only does, and that counts the attempts.
type failingSaveStore struct {
	account.Store
	mu    sync.Mutex
	fail  bool
	tries int
}

func (s *failingSaveStore) Save(sess *account.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tries++
	if s.fail {
		return errors.New("simulated: the disk is full")
	}
	return s.Store.Save(sess)
}

func (s *failingSaveStore) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tries
}

// touchHW reads the session again under the lock, and what it reads is the newest
// thing this process has seen: another process may have written it within the
// file's modification-time tick, so the poll that compares times finds nothing
// new. When the high-water mark write that follows fails, the guard must still
// take that session in, not drop it until some later write succeeds.
func TestAGuardWhoseHighWaterWriteFailsStillTakesInTheSessionItReadUnderTheLock(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy, valid for 50 more minutes; its mark is ten minutes old, so touchHW will try
	fs := &failingSaveStore{Store: account.OpenStore(e.dir, e.seal)}
	g := e.guardOver(fs, 0)
	now := e.f.Clock.Now()
	if st := g.Status(); st.State != account.StateOK || !st.ValidUntil.Equal(now.Add(50*time.Minute)) {
		t.Fatalf("Status = %s valid until %v, want the stored token (50 minutes left)", st.State, st.ValidUntil)
	}
	seen := mustMtime(t, e.store)

	// Another process refreshed: a later token, with a mark that is stale too (it
	// is the token's iat, ten minutes ago), written within the same tick.
	later, err := account.NewSession(account.HostURL, e.f.Token(accounttest.TokenOptions{IssuedAt: now.Add(-10 * time.Minute), Lifetime: 2 * time.Hour}), &account.User{ID: "user-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.Save(later); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(e.dir, "session.json"), seen, seen); err != nil {
		t.Fatal(err)
	}
	if !mustMtime(t, e.store).Equal(seen) {
		t.Fatal("the test could not restore the file's modification time: it cannot tell a poll from the lock's read")
	}
	fs.fail = true

	st, err := g.EnsureFresh(context.Background())
	if err != nil {
		t.Fatalf("EnsureFresh = %v: a high-water write that fails is not reported", err)
	}
	if n := fs.attempts(); n != 1 {
		t.Fatalf("%d writes of the mark, want 1: this test cannot tell without the failing write", n)
	}
	if !st.ValidUntil.Equal(now.Add(110 * time.Minute)) {
		t.Fatalf("EnsureFresh = %s valid until %v, want the other process's token (110 minutes left)", st.State, st.ValidUntil)
	}
	if st := g.Status(); st.State != account.StateOK || !st.ValidUntil.Equal(now.Add(110*time.Minute)) {
		t.Fatalf("Status afterwards = %s valid until %v, want the other process's token", st.State, st.ValidUntil)
	}
	if !mustMtime(t, e.store).Equal(seen) {
		t.Fatal("the file was written although the write was made to fail")
	}
}
