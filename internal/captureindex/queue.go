package captureindex

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// DefaultRetryDelays is how long Queue waits before each retry of a
// profile whose pass left a capture failed. Three retries over about ten
// minutes ride out a monomind that was busy or mid-upgrade; after that the
// error stays on the row and `profile documents index --all` (or the next
// bridge start) tries again.
var DefaultRetryDelays = []time.Duration{20 * time.Second, 2 * time.Minute, 8 * time.Minute}

// OpenDB opens the monoagent database for one pass and returns its closer.
type OpenDB func() (*sql.DB, func(), error)

// Queue runs indexing passes in the background for the process that
// writes captures (the extension bridge). Enqueue is cheap and never
// blocks: a capture is acknowledged as fast as it was before indexing
// existed. Passes run one at a time, and several captures landing for one
// profile while a pass runs coalesce into one more pass.
type Queue struct {
	Indexer *Indexer
	Open    OpenDB
	// Logf receives one line per pass that did something. Nil is silent.
	Logf func(string, ...any)
	// RetryDelays overrides DefaultRetryDelays (tests).
	RetryDelays []time.Duration

	mu       sync.Mutex
	pending  map[string]bool // profile id -> retry failed rows too
	order    []string
	attempts map[string]int // profile id -> retries used since its last clean pass
	timers   map[string]*time.Timer
	wake     chan struct{}
	stopped  bool

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
}

// Start runs the worker until Stop. A stopped queue never starts.
func (q *Queue) Start() {
	q.startOnce.Do(func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		q.init()
		if q.stopped {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		q.cancel = cancel
		q.done = make(chan struct{})
		go q.run(ctx, q.done)
	})
}

// Stop cancels the running pass, drops pending work and waits for the
// worker. Safe to call more than once, and before Start.
func (q *Queue) Stop() {
	q.stopOnce.Do(func() {
		q.mu.Lock()
		q.stopped = true
		for _, t := range q.timers {
			t.Stop()
		}
		cancel, done := q.cancel, q.done
		q.mu.Unlock()
		if cancel != nil {
			cancel()
			<-done
		}
	})
}

func (q *Queue) init() {
	if q.pending == nil {
		q.pending = map[string]bool{}
		q.attempts = map[string]int{}
		q.timers = map[string]*time.Timer{}
		q.wake = make(chan struct{}, 1)
	}
}

// Enqueue asks for a pass over profileID's captures. An empty or unusable
// id (a capture saved to the shared inbox) is ignored: those captures
// belong to no profile's store.
func (q *Queue) Enqueue(profileID string) { q.enqueue(profileID, false) }

// EnqueueRetry asks for a pass that also retries failed rows.
func (q *Queue) EnqueueRetry(profileID string) { q.enqueue(profileID, true) }

func (q *Queue) enqueue(profileID string, retry bool) {
	id := strings.TrimSpace(profileID)
	if !profiledir.ValidProfileID(id) {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return
	}
	q.init()
	was, queued := q.pending[id]
	if !queued {
		q.order = append(q.order, id)
	}
	q.pending[id] = was || retry
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *Queue) next() (string, bool, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.order) == 0 {
		return "", false, false
	}
	id := q.order[0]
	q.order = q.order[1:]
	retry := q.pending[id]
	delete(q.pending, id)
	return id, retry, true
}

func (q *Queue) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		for {
			id, retry, ok := q.next()
			if !ok {
				break
			}
			q.pass(ctx, id, retry)
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-q.wake:
		}
	}
}

func (q *Queue) pass(ctx context.Context, profileID string, retry bool) {
	db, closeDB, err := q.Open()
	if err != nil {
		q.logf("capture index: %s: cannot open the database: %v", profileID, err)
		q.scheduleRetry(profileID)
		return
	}
	report, err := q.Indexer.IndexProfile(ctx, db, profileID, Options{RetryFailed: retry})
	closeDB()
	if err != nil {
		if ctx.Err() == nil {
			q.logf("capture index: %s: %v", profileID, err)
			q.scheduleRetry(profileID)
		}
		return
	}
	for _, o := range report.Outcomes {
		if o.Indexed {
			q.logf("capture index: %s: indexed %s (%s)", profileID, o.Title, o.ID)
		} else {
			q.logf("capture index: %s: could not index %s (%s): %s", profileID, o.Title, o.ID, o.Error)
		}
	}
	if report.Failed > 0 {
		q.scheduleRetry(profileID)
		return
	}
	if report.Waiting == 0 {
		q.mu.Lock()
		delete(q.attempts, profileID)
		q.mu.Unlock()
	}
}

// scheduleRetry queues a retry pass after the next delay, until the delays
// run out.
func (q *Queue) scheduleRetry(profileID string) {
	delays := q.RetryDelays
	if delays == nil {
		delays = DefaultRetryDelays
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.stopped {
		return
	}
	n := q.attempts[profileID]
	if n >= len(delays) {
		return
	}
	if _, waiting := q.timers[profileID]; waiting {
		return
	}
	q.attempts[profileID] = n + 1
	q.timers[profileID] = time.AfterFunc(delays[n], func() {
		q.mu.Lock()
		delete(q.timers, profileID)
		q.mu.Unlock()
		q.EnqueueRetry(profileID)
	})
}

func (q *Queue) logf(format string, args ...any) {
	if q.Logf != nil {
		q.Logf(format, args...)
	}
}
