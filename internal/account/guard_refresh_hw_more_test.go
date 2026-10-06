package account_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// Another process refuses the session and leaves session.json with the
// modification time it had: a filesystem with coarse timestamps does that when
// the two writes fall in one tick. The guard's poll then finds an unchanged
// file, so the one read that can still show it the refusal is the one touchHW
// makes under the lock. It must take what it found in, not drop it.
func TestAGuardThatFindsARefusalWhileWritingTheHighWaterMarkTakesItIn(t *testing.T) {
	e := newEnv(t)
	e.signIn(10*time.Minute, time.Hour) // healthy, and its mark is ten minutes old: touchHW will try
	if st := e.g.Status(); st.State != account.StateOK {
		t.Fatalf("Status = %s/%q, want ok", st.State, st.Reason)
	}
	seen := mustMtime(t, e.store)
	e.refuse()
	if err := os.Chtimes(filepath.Join(e.dir, "session.json"), seen, seen); err != nil {
		t.Fatal(err)
	}
	if !mustMtime(t, e.store).Equal(seen) {
		t.Fatal("the test could not restore the file's modification time: it cannot tell a poll from the lock's read")
	}

	st, err := e.g.EnsureFresh(context.Background())
	if err != nil || st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Fatalf("EnsureFresh = %s/%q, %v, want locked/refused: the refusal was read under the lock", st.State, st.Reason, err)
	}
	if st := e.g.Status(); st.State != account.StateLocked || st.Reason != account.ReasonRefused {
		t.Fatalf("Status afterwards = %s/%q, want locked/refused", st.State, st.Reason)
	}
	if n := e.ref.calls.Load(); n != 0 {
		t.Fatalf("%d network refreshes, want none: a refused session never tries again", n)
	}
}
