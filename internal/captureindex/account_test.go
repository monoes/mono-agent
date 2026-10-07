package captureindex_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

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
