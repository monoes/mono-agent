package automation

import (
	"database/sql"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monoes/mono-agent/internal/action"
)

// Selector health recording (spec §8.7). The executor calls ObserveSelector
// on every package-selector lookup, so the call must never block, slow a run
// down or fail it: observations go into a bounded channel (dropped and
// counted when it is full) and a background goroutine aggregates them and
// writes one transaction per batch. Healing promotions run from the same
// goroutine, after the write — never on the run's path.

// SelectorPromoter promotes a healed selector candidate (spec §8.7),
// identified by content: the index a run reports is relative to the entry
// that run saw, which a promotion may since have reordered. The registry
// implements it (health_promote.go).
type SelectorPromoter interface {
	PromoteCandidate(automationID, key string, c action.SelectorCandidate) error
}

// HealthOptions tunes a HealthRecorder. Zero values take the defaults.
type HealthOptions struct {
	Buffer   int           // queued observations before dropping (default 1024)
	Interval time.Duration // flush interval (default 2s)
	MaxBatch int           // flush early at this many pending observations (default 256)
	Promoter SelectorPromoter
}

// HealthRecorder is the concrete selector observer behind HealthObserver.
type HealthRecorder struct {
	db       *sql.DB
	opts     HealthOptions
	ch       chan healthObservation
	flushReq chan chan error
	done     chan struct{}
	stopped  chan struct{}
	closed   atomic.Bool
	dropped  atomic.Int64
	once     sync.Once
	now      func() time.Time

	testHookWrite func() // tests: called before each batch write
}

type healthKey struct{ id, key string }

type healthObservation struct {
	healthKey
	idx        int
	cand       *action.SelectorCandidate // the matched candidate, when the executor reports it
	ok, healed bool
	at         time.Time
}

// healthDelta aggregates one batch of observations for one selector.
type healthDelta struct {
	ok, fail, healed int
	lastOK, lastFail time.Time
	lastIdx          int
	recent           []byte
	promote          *action.SelectorCandidate // healed candidate to move first, or nil
}

var _ action.SelectorObserver = (*HealthRecorder)(nil)

// The executor calls ObserveSelectorCandidate instead of ObserveSelector
// when the observer implements it, so promotions get the candidate itself.
var _ action.SelectorCandidateObserver = (*HealthRecorder)(nil)

// HealthObserver returns the selector-health observer backed by the
// automation_selector_health table (spec §8.7). Healed candidates are
// promoted through the registry the process booted (the one installed as
// the action loader's DefSource); with none booted, nothing is promoted —
// never a registry under some other home. The returned value is a
// *HealthRecorder: callers that own its lifetime should type-assert to
// interface{ Close() error } and close it on shutdown so the last batch is
// written.
func HealthObserver(db *sql.DB) action.SelectorObserver {
	return NewHealthRecorder(db, HealthOptions{Promoter: bootedPromoter{}})
}

// NewHealthRecorder starts a recorder writing to db.
func NewHealthRecorder(db *sql.DB, opts HealthOptions) *HealthRecorder {
	if opts.Buffer <= 0 {
		opts.Buffer = 1024
	}
	if opts.Interval <= 0 {
		opts.Interval = 2 * time.Second
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = 256
	}
	r := &HealthRecorder{
		db:       db,
		opts:     opts,
		ch:       make(chan healthObservation, opts.Buffer),
		flushReq: make(chan chan error),
		done:     make(chan struct{}),
		stopped:  make(chan struct{}),
		now:      time.Now,
	}
	go r.loop()
	return r
}

// ObserveSelector implements action.SelectorObserver. It never blocks.
// Without the matched candidate, the outcome is counted but nothing is
// promoted.
func (r *HealthRecorder) ObserveSelector(automationID, key string, candidateIndex int, ok, healed bool) {
	r.ObserveSelectorCandidate(automationID, key, nil, candidateIndex, ok, healed)
}

// ObserveSelectorCandidate is ObserveSelector plus the matched candidate
// (nil when none matched or the Jev fallback found the element). It never
// blocks.
func (r *HealthRecorder) ObserveSelectorCandidate(automationID, key string, c *action.SelectorCandidate, candidateIndex int, ok, healed bool) {
	if r == nil || automationID == "" || key == "" {
		return
	}
	if r.closed.Load() {
		r.dropped.Add(1)
		return
	}
	o := healthObservation{healthKey: healthKey{automationID, key}, idx: candidateIndex, ok: ok, healed: healed, at: r.now()}
	if c != nil && ok {
		cc := *c
		o.cand = &cc
	}
	select {
	case r.ch <- o:
	default:
		r.dropped.Add(1)
	}
}

// Dropped is the number of observations lost to a full queue or a closed
// recorder.
func (r *HealthRecorder) Dropped() int64 { return r.dropped.Load() }

// errRecorderClosed is returned by Flush after Close.
var errRecorderClosed = errors.New("automation: health recorder closed")

// Flush writes everything queued so far and waits for it.
func (r *HealthRecorder) Flush() error {
	reply := make(chan error, 1)
	select {
	case r.flushReq <- reply:
		return <-reply
	case <-r.stopped:
		return errRecorderClosed
	}
}

// Close writes what is queued and stops the recorder. Later observations
// are dropped. Safe to call more than once.
func (r *HealthRecorder) Close() error {
	r.once.Do(func() {
		r.closed.Store(true)
		close(r.done)
	})
	<-r.stopped
	return nil
}

func (r *HealthRecorder) loop() {
	defer close(r.stopped)
	ticker := time.NewTicker(r.opts.Interval)
	defer ticker.Stop()
	pending := map[healthKey]*healthDelta{}
	n := 0
	add := func(o healthObservation) {
		addObservation(pending, o)
		n++
	}
	drain := func() {
		for {
			select {
			case o := <-r.ch:
				add(o)
			default:
				return
			}
		}
	}
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		batch := pending
		pending, n = map[healthKey]*healthDelta{}, 0
		if r.testHookWrite != nil {
			r.testHookWrite()
		}
		err := writeHealthBatch(r.db, batch, r.now())
		if err != nil {
			log.Printf("automation: selector health: write batch: %v", err)
		}
		r.promote(batch)
		return err
	}
	for {
		select {
		case o := <-r.ch:
			add(o)
			if n >= r.opts.MaxBatch {
				_ = flush()
			}
		case <-ticker.C:
			_ = flush()
		case reply := <-r.flushReq:
			drain()
			reply <- flush()
		case <-r.done:
			drain()
			_ = flush()
			return
		}
	}
}

// addObservation folds o into the batch.
func addObservation(pending map[healthKey]*healthDelta, o healthObservation) {
	d := pending[o.healthKey]
	if d == nil {
		d = &healthDelta{}
		pending[o.healthKey] = d
	}
	d.lastIdx = o.idx
	d.recent = append(d.recent, outcomeOf(o.ok, o.healed))
	if !o.ok {
		d.fail++
		d.lastFail = o.at
		return
	}
	d.ok++
	d.lastOK = o.at
	if o.healed {
		d.healed++
	}
	// The latest success decides: a later first-candidate match means the
	// first candidate works again and nothing needs promoting.
	d.promote = nil
	if o.healed && o.cand != nil {
		d.promote = o.cand
	}
}

// promote applies healing promotions for a written batch. A promotion
// moves a candidate by content, so a stale report (a run that started
// before an earlier promotion) is a no-op, never a flip back.
func (r *HealthRecorder) promote(batch map[healthKey]*healthDelta) {
	if r.opts.Promoter == nil {
		return
	}
	for k, d := range batch {
		if d.promote == nil {
			continue
		}
		if err := r.opts.Promoter.PromoteCandidate(k.id, k.key, *d.promote); err != nil {
			log.Printf("automation: selector health: promote %s/%s: %v", k.id, k.key, err)
		}
	}
}

const healthUpsertSQL = `
INSERT INTO automation_selector_health
    (automation_id, selector_key, ok_count, fail_count, healed_count,
     last_ok_at, last_fail_at, last_candidate_index, recent, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(automation_id, selector_key) DO UPDATE SET
    ok_count             = ok_count + excluded.ok_count,
    fail_count           = fail_count + excluded.fail_count,
    healed_count         = healed_count + excluded.healed_count,
    last_ok_at           = COALESCE(excluded.last_ok_at, last_ok_at),
    last_fail_at         = COALESCE(excluded.last_fail_at, last_fail_at),
    last_candidate_index = excluded.last_candidate_index,
    recent               = substr(recent || excluded.recent, -10),
    updated_at           = excluded.updated_at`

// writeHealthBatch upserts a batch in one transaction.
func writeHealthBatch(db *sql.DB, batch map[healthKey]*healthDelta, now time.Time) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(healthUpsertSQL)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for k, d := range batch {
		if _, err := stmt.Exec(k.id, k.key, d.ok, d.fail, d.healed,
			nullTime(d.lastOK), nullTime(d.lastFail), d.lastIdx,
			appendRecent("", string(d.recent)), formatHealthTime(now)); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func formatHealthTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatHealthTime(t)
}

// bootedPromoter promotes through the registry behind the current
// DefSource, resolved at promotion time (boot may happen after the
// observer is created).
type bootedPromoter struct{}

func (bootedPromoter) PromoteCandidate(id, key string, c action.SelectorCandidate) error {
	reg := bootedRegistry()
	if reg == nil {
		return nil
	}
	return reg.PromoteCandidate(id, key, c)
}

// bootedRegistry returns the registry installed via action.SetDefSource, or nil.
func bootedRegistry() *Registry {
	if ds, ok := action.CurrentDefSource().(*defSource); ok && ds != nil {
		return ds.r
	}
	return nil
}
