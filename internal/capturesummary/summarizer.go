package capturesummary

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// Answer is what a runtime turn produced.
type Answer struct {
	Text    string
	CostUSD float64
}

// RunFunc asks runtime for a completion of prompt. The production one is
// ExecRunner (monomind agent exec); tests substitute a stub.
type RunFunc func(ctx context.Context, runtime, prompt string) (Answer, error)

// DefaultRuntime is the agent runtime summaries use unless configured.
const DefaultRuntime = "claude"

// Disabled runtime values: a bridge configured with one of these records
// every requested summary as an error saying so, rather than ignoring it.
func isDisabled(runtime string) bool {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "off", "none", "disabled", "0", "false":
		return true
	}
	return false
}

// DefaultTimeout bounds one summary turn.
const DefaultTimeout = 5 * time.Minute

// queueDepth is how many summaries may wait behind the one running. A
// person saving pages by hand never gets near it; a batch capture of fifty
// tabs with "summarize" on would, and the overflow is recorded as an error
// on each capture rather than piling up agent processes.
const queueDepth = 16

type job struct {
	dir  string
	meta capture.Meta
	kind string
}

// Summarizer runs capture summaries one at a time in the background.
type Summarizer struct {
	Runtime       string
	Run           RunFunc
	MaxInputChars int
	Timeout       time.Duration
	// Now supplies the time; nil means time.Now.
	Now func() time.Time
	// Logf, when set, receives one line per summary outcome.
	Logf func(format string, args ...any)
	// OnDone, when set, is called after each summary finishes (tests).
	OnDone func(dir string, st Status)

	once   sync.Once
	queue  chan job
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns a summarizer for runtime ("" means DefaultRuntime).
func New(runtime string, run RunFunc) *Summarizer {
	if strings.TrimSpace(runtime) == "" {
		runtime = DefaultRuntime
	}
	return &Summarizer{Runtime: strings.TrimSpace(runtime), Run: run}
}

func (s *Summarizer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Summarizer) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func (s *Summarizer) start() {
	s.once.Do(func() {
		s.queue = make(chan job, queueDepth)
		s.ctx, s.cancel = context.WithCancel(context.Background())
		s.wg.Add(1)
		go s.worker()
	})
}

// Close stops the worker, cancelling a summary in flight. Summaries still
// queued keep their "pending" status; a reader treats them as stalled
// after StaleAfter.
func (s *Summarizer) Close() {
	if s == nil {
		return
	}
	s.start() // so there is always something to cancel
	s.cancel()
	s.wg.Wait()
}

// Handle is the extension server's after-write hook: a capture that asks
// for a summary gets one queued; every other capture is left alone.
func (s *Summarizer) Handle(res *capture.Result) {
	if s == nil || res == nil {
		return
	}
	kind, ok := Requested(res.Meta)
	if !ok {
		return
	}
	if err := s.Enqueue(res.Path, res.Meta, kind); err != nil {
		s.logf("summary not queued for %s: %v", res.Path, err)
	}
}

// Enqueue records the summary as pending and queues it. It never blocks:
// with the queue full, or summaries turned off, the reason is written to
// summary.json and returned.
func (s *Summarizer) Enqueue(dir string, meta capture.Meta, kind string) error {
	st := Status{Status: StatePending, Kind: kind, Runtime: s.Runtime, RequestedAt: stamp(s.now())}
	if isDisabled(s.Runtime) {
		st.Status, st.Runtime = StateError, ""
		st.Error = "summaries are turned off on this bridge (MONOAGENT_SUMMARY_RUNTIME / --summary-runtime)"
		st.FinishedAt = st.RequestedAt
		return errors.Join(errors.New(st.Error), writeStatus(dir, st))
	}
	if s.Run == nil {
		st.Status, st.Error, st.FinishedAt = StateError, "no summary runner is configured", st.RequestedAt
		return errors.Join(errors.New(st.Error), writeStatus(dir, st))
	}
	if err := writeStatus(dir, st); err != nil {
		return err
	}
	s.start()
	select {
	case s.queue <- job{dir: dir, meta: meta, kind: kind}:
		return nil
	default:
		st.Status, st.FinishedAt = StateError, stamp(s.now())
		st.Error = fmt.Sprintf("too many summaries waiting (%d); this one was skipped", queueDepth)
		return errors.Join(errors.New(st.Error), writeStatus(dir, st))
	}
}

func (s *Summarizer) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.queue:
			st := s.Summarize(s.ctx, j.dir, j.meta, j.kind)
			if st.Status == StateDone {
				s.logf("summary written for %s (%s, $%.2f)", j.dir, s.Runtime, st.CostUSD)
			} else {
				s.logf("summary failed for %s: %s", j.dir, st.Error)
			}
			if s.OnDone != nil {
				s.OnDone(j.dir, st)
			}
		}
	}
}

// Summarize writes one capture's summary now, synchronously, and returns
// the status it recorded. The capture is never modified beyond summary.md
// and summary.json.
func (s *Summarizer) Summarize(ctx context.Context, dir string, meta capture.Meta, kind string) Status {
	st := Status{Status: StateRunning, Kind: kind, Runtime: s.Runtime, StartedAt: stamp(s.now())}
	if prev, err := ReadStatus(dir); err == nil {
		st.RequestedAt = prev.RequestedAt
	}
	fail := func(err error) Status {
		st.Status, st.Error, st.FinishedAt = StateError, err.Error(), stamp(s.now())
		if werr := writeStatus(dir, st); werr != nil {
			st.Error += "; and recording that failed: " + werr.Error()
		}
		return st
	}

	in, err := LoadInput(dir, meta, kind, s.MaxInputChars)
	if err != nil {
		return fail(err)
	}
	st.Source, st.InputChars, st.Truncated = in.Source, len([]rune(in.Content)), in.Truncated
	if err := writeStatus(dir, st); err != nil {
		return fail(err)
	}

	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	turnCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	answer, err := s.Run(turnCtx, s.Runtime, Prompt(in))
	st.CostUSD = answer.CostUSD
	if err != nil {
		if errors.Is(turnCtx.Err(), context.DeadlineExceeded) {
			return fail(fmt.Errorf("%s did not answer within %s", s.Runtime, timeout))
		}
		return fail(fmt.Errorf("%s: %w", s.Runtime, err))
	}
	if strings.TrimSpace(answer.Text) == "" {
		return fail(fmt.Errorf("%s returned an empty summary", s.Runtime))
	}
	doc := Document(in, s.Runtime, answer.Text, s.now().UTC().Format("2006-01-02"))
	if err := writeAtomic(dir, SummaryFile, []byte(doc)); err != nil {
		return fail(fmt.Errorf("write %s: %w", SummaryFile, err))
	}
	st.Status, st.FinishedAt = StateDone, stamp(s.now())
	if err := writeStatus(dir, st); err != nil {
		return fail(err)
	}
	return st
}
