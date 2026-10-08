package captureindex_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/captureindex"
)

// A locked account indexes nothing, whatever asked for the pass: a new
// capture, the bridge's catch-up after it starts (EnqueueRetry, as here), a
// retry or the indexing after a summary. The pass says why and runs no
// monomind; in every other state it indexes the capture.
func TestTheQueueIndexesNothingWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			db := newTestDB(t)
			a := writeCapture(t, work, "a", "alpha")
			rec := &recorder{}
			lines := make(chan string, 16)
			q := &captureindex.Queue{
				Indexer: &captureindex.Indexer{Ingest: rec.ingest},
				Open:    func() (*sql.DB, func(), error) { return db, func() {}, nil },
				Logf:    func(format string, args ...any) { lines <- fmt.Sprintf(format, args...) },
			}
			accounttest.Install(t, c.Mode)
			q.Start()
			defer q.Stop()

			q.EnqueueRetry(work)
			var line string
			select {
			case line = <-lines:
			case <-time.After(5 * time.Second):
				t.Fatal("the pass never ran")
			}
			if skipped := strings.Contains(line, "no valid monoes.me login"); skipped != c.Refused {
				t.Fatalf("the pass logged %q, want skipped = %v", line, c.Refused)
			}
			if c.Refused {
				if n := rec.count(); n != 0 {
					t.Fatalf("%d captures were ingested while locked", n)
				}
				return
			}
			if !docByPath(t, db, work, a).Indexed {
				t.Fatal("the capture was not indexed")
			}
		})
	}
}

// A capture written while locked is not lost: the pass that skipped it left it
// unindexed, and the profile's next pass after a sign-in takes it up (Task 7b,
// issue #368 item 3).
func TestACaptureLeftUnindexedWhileLockedIsIndexedAfterSignIn(t *testing.T) {
	db := newTestDB(t)
	a := writeCapture(t, work, "a", "alpha")
	rec := &recorder{}
	lines := make(chan string, 16)
	q := &captureindex.Queue{
		Indexer: &captureindex.Indexer{Ingest: rec.ingest},
		Open:    func() (*sql.DB, func(), error) { return db, func() {}, nil },
		Logf:    func(format string, args ...any) { lines <- fmt.Sprintf(format, args...) },
	}
	_, f := accounttest.InstallWithFixture(t, accounttest.LockedNoLogin)
	q.Start()
	defer q.Stop()

	wait := func() string {
		t.Helper()
		select {
		case l := <-lines:
			return l
		case <-time.After(5 * time.Second):
			t.Fatal("the pass never ran")
			return ""
		}
	}
	q.Enqueue(work)
	if l := wait(); !strings.Contains(l, "no valid monoes.me login") {
		t.Fatalf("locked pass logged %q", l)
	}
	if rec.count() != 0 {
		t.Fatal("a capture was ingested while locked")
	}

	// Sign in: a guard over a store that holds a session.
	now := f.Clock.Now()
	sess, err := account.NewSession(account.HostURL, f.Token(accounttest.TokenOptions{IssuedAt: now}),
		&account.User{ID: "user-1", Email: "user@example.test", Username: "user"}, now)
	if err != nil {
		t.Fatal(err)
	}
	store := account.OpenStore(t.TempDir(), account.NewMemorySealer())
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}
	g := account.NewGuard(account.GuardOptions{Store: store, Now: f.Clock.Now})
	t.Cleanup(g.Close)
	account.InstallForTest(t, g)

	q.Enqueue(work) // the profile's next pass: a new capture, a restart's catch-up
	for {
		if l := wait(); strings.Contains(l, "indexed") {
			break
		}
	}
	if !docByPath(t, db, work, a).Indexed {
		t.Fatal("the capture written while locked was not indexed after sign-in")
	}
}
