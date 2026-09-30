package agentroster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Automatic re-validation (monoes/mono-agent#230): the daemon re-checks
// stale roster entries in the background, one runtime at a time, only while
// nothing else runs, at most a few runtimes a day. Every check is a real,
// paid model call, so it is off until the user turns it on.

// Auto re-validation defaults and limits.
const (
	DefaultAutoRuntimesPerDay = 1
	DefaultAutoModelsPerRun   = 3
	DefaultAutoQuietPeriod    = 15 * time.Minute
	MaxAutoRuntimesPerDay     = 24
	MaxAutoModelsPerRun       = 20
	MinAutoQuietPeriod        = time.Minute
)

// Settings keys (global `settings` table: the roster is machine-wide).
const (
	autoConfigKey = "agent_roster.auto_revalidate"
	autoStateKey  = "agent_roster.auto_revalidate.state"
)

// AutoConfig is the user's auto re-validation setting.
type AutoConfig struct {
	Enabled           bool          `json:"enabled"`
	MaxRuntimesPerDay int           `json:"max_runtimes_per_day"`
	MaxModelsPerRun   int           `json:"max_models_per_run"`
	QuietPeriod       time.Duration `json:"quiet_period_ns"`
}

// DefaultAutoConfig is the setting before the user changes it: off.
func DefaultAutoConfig() AutoConfig {
	return AutoConfig{
		MaxRuntimesPerDay: DefaultAutoRuntimesPerDay,
		MaxModelsPerRun:   DefaultAutoModelsPerRun,
		QuietPeriod:       DefaultAutoQuietPeriod,
	}
}

// Normalize fills zero values with the defaults and clamps to the limits.
func (c AutoConfig) Normalize() AutoConfig {
	d := DefaultAutoConfig()
	if c.MaxRuntimesPerDay <= 0 {
		c.MaxRuntimesPerDay = d.MaxRuntimesPerDay
	}
	c.MaxRuntimesPerDay = min(c.MaxRuntimesPerDay, MaxAutoRuntimesPerDay)
	if c.MaxModelsPerRun <= 0 {
		c.MaxModelsPerRun = d.MaxModelsPerRun
	}
	c.MaxModelsPerRun = min(c.MaxModelsPerRun, MaxAutoModelsPerRun)
	if c.QuietPeriod <= 0 {
		c.QuietPeriod = d.QuietPeriod
	}
	c.QuietPeriod = max(c.QuietPeriod, MinAutoQuietPeriod)
	return c
}

// AutoState is what the scheduler did, persisted so the daily cap survives
// a daemon restart and `auto-revalidate status` can show it.
type AutoState struct {
	Day             string    `json:"day"` // local date (daemon's time zone) the counts below belong to
	RuntimesToday   int       `json:"runtimes_today"`
	SpentTodayUSD   float64   `json:"spent_today_usd"`
	UnknownCostCall int       `json:"unknown_cost_calls_today"` // calls that reported no cost
	LastRunAt       time.Time `json:"last_run_at,omitempty"`
	LastRuntime     string    `json:"last_runtime,omitempty"`
	LastSummary     *Summary  `json:"last_summary,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	// LastCheckAt/LastCheck: the scheduler's latest decision ("off",
	// "busy: …", "quiet", "daily cap reached", "nothing stale", "ran").
	LastCheckAt    time.Time `json:"last_check_at,omitempty"`
	LastCheck      string    `json:"last_check,omitempty"`
	NextEligibleAt time.Time `json:"next_eligible_at,omitempty"`
}

// rollDay resets the daily counts when now is on a later local day.
func (s *AutoState) rollDay(now time.Time) {
	day := now.Local().Format("2006-01-02")
	if s.Day != day {
		s.Day, s.RuntimesToday, s.SpentTodayUSD, s.UnknownCostCall = day, 0, 0, 0
	}
}

// LoadAutoConfig reads the setting; missing means DefaultAutoConfig (off).
func LoadAutoConfig(ctx context.Context, db *sql.DB) (AutoConfig, error) {
	c := DefaultAutoConfig()
	raw, ok, err := getSetting(ctx, db, autoConfigKey)
	if err != nil || !ok {
		return c, err
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return DefaultAutoConfig(), fmt.Errorf("reading %s: %w", autoConfigKey, err)
	}
	return c.Normalize(), nil
}

// SaveAutoConfig stores the setting.
func SaveAutoConfig(ctx context.Context, db *sql.DB, c AutoConfig) error {
	b, _ := json.Marshal(c.Normalize())
	return setSetting(ctx, db, autoConfigKey, string(b))
}

// LoadAutoState reads the scheduler's state, with today's counts rolled
// over when the stored day is past.
func LoadAutoState(ctx context.Context, db *sql.DB, now time.Time) (AutoState, error) {
	var s AutoState
	raw, ok, err := getSetting(ctx, db, autoStateKey)
	if err == nil && ok {
		err = json.Unmarshal([]byte(raw), &s)
	}
	s.rollDay(now)
	return s, err
}

// SaveAutoState stores the scheduler's state.
func SaveAutoState(ctx context.Context, db *sql.DB, s AutoState) error {
	b, _ := json.Marshal(s)
	return setSetting(ctx, db, autoStateKey, string(b))
}

func getSetting(ctx context.Context, db *sql.DB, key string) (string, bool, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func setSetting(ctx context.Context, db *sql.DB, key, value string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// AutoPlan is one automatic run: the stale models of one runtime and the
// estimated cost from their earlier results.
type AutoPlan struct {
	Runtime     string   `json:"runtime"`
	Targets     []Target `json:"targets"`
	EstCostUSD  float64  `json:"est_cost_usd"`
	UnknownCost int      `json:"unknown_cost"`
	// TableEstimated counts targets priced from the built-in table.
	TableEstimated int `json:"table_estimated"`
}

// PickStale chooses the next automatic run from a built roster: the
// installed runtime whose oldest stale model is oldest, and up to maxModels
// of its stale models, oldest first. Only stale entries (passed once, now
// too old or on another runtime version) are picked: failed and untested
// ones need the user. version is the runtime's installed version, recorded
// on the targets. Nil when nothing is stale.
func PickStale(roster []RuntimeRoster, previous []Result, maxModels int) *AutoPlan {
	if maxModels <= 0 {
		maxModels = DefaultAutoModelsPerRun
	}
	var best *RuntimeRoster
	var bestStale []Entry
	for i := range roster {
		rr := &roster[i]
		if !rr.Installed {
			continue
		}
		var stale []Entry
		for _, e := range rr.Models {
			if e.State == StateStale {
				stale = append(stale, e)
			}
		}
		if len(stale) == 0 {
			continue
		}
		sort.SliceStable(stale, func(a, b int) bool { return stale[a].ValidatedAt.Before(stale[b].ValidatedAt) })
		if best == nil || stale[0].ValidatedAt.Before(bestStale[0].ValidatedAt) {
			best, bestStale = rr, stale
		}
	}
	if best == nil {
		return nil
	}
	if len(bestStale) > maxModels {
		bestStale = bestStale[:maxModels]
	}
	p := &AutoPlan{Runtime: best.Runtime}
	for _, e := range bestStale {
		p.Targets = append(p.Targets, Target{
			Runtime: best.Runtime, Model: e.Model, Label: e.Label, EffortLevels: e.EffortLevels,
			Source: orDefault(e.Source, SourceListed), RuntimeVersion: best.Version,
		})
	}
	plan := Plan{Targets: p.Targets}
	plan.EstimateCost(previous)
	p.EstCostUSD, p.UnknownCost, p.TableEstimated = plan.EstCostUSD, plan.UnknownCost, plan.TableEstimated
	return p
}

// DailyCeiling is the most a day of automatic runs can cost: every run
// at the cap, every model priced like the priciest model with a known cost
// in previous. known is false when no model has reported a cost yet.
func DailyCeiling(c AutoConfig, previous []Result) (perDay, priciest float64, known bool) {
	c = c.Normalize()
	for _, r := range previous {
		if r.HasCost && r.CostUSD > priciest {
			priciest, known = r.CostUSD, true
		}
	}
	return float64(c.MaxRuntimesPerDay*c.MaxModelsPerRun) * priciest, priciest, known
}

// AutoRunResult is what one automatic run did.
type AutoRunResult struct {
	Summary     Summary
	SpentUSD    float64 // reported (or estimated) cost of the calls made
	UnknownCost int     // calls that reported no cost
}

// AutoScheduler runs automatic re-validation. Every dependency is a func so
// tests run it with a fake clock and a fake validator.
type AutoScheduler struct {
	DB  *sql.DB
	Now func() time.Time
	// Busy reports whether a chat turn, workflow run or org run is active,
	// with a short reason.
	Busy func(ctx context.Context) (bool, string)
	// Pick returns the next run (nil when nothing is stale). It runs
	// before the lock is taken: a scan must not block a manual validate.
	Pick func(ctx context.Context, maxModels int) (*AutoPlan, error)
	// Validate runs the plan's tests, one at a time. Its ctx is cancelled
	// when the app gets busy or the setting is turned off mid-run.
	Validate func(ctx context.Context, p AutoPlan) (AutoRunResult, error)
	// Lock takes the validation lock `agent validate` also takes; ok false
	// means another validation runs.
	Lock func() (release func(), ok bool)
	// Interval between checks; 1 minute when zero.
	Interval time.Duration
	// WatchInterval is how often a run in progress re-checks Busy and the
	// setting; 5 seconds when zero.
	WatchInterval time.Duration
	// NothingBackoff is how long to wait after "nothing stale" before
	// planning (and scanning) again; 1 hour when zero.
	NothingBackoff time.Duration
	Logf           func(format string, args ...any)

	mu        sync.Mutex
	lastBusy  time.Time // start, last busy check, or last run: the quiet period counts from here
	planAfter time.Time // no planning before this (backoff after nothing stale)
	started   bool
}

// Check outcomes.
const (
	AutoCheckOff        = "off"
	AutoCheckBusy       = "busy"
	AutoCheckQuiet      = "waiting for quiet period"
	AutoCheckCap        = "daily cap reached"
	AutoCheckLocked     = "another validation is running"
	AutoCheckNothing    = "nothing stale"
	AutoCheckRan        = "ran"
	AutoCheckCancelled  = "cancelled"
	AutoCheckFailed     = "failed"
	autoDefaultInterval = time.Minute
	autoDefaultWatch    = 5 * time.Second
	autoDefaultBackoff  = time.Hour
)

// Why a run in progress was cancelled.
var (
	errAutoBusy      = errors.New("the app got busy")
	errAutoTurnedOff = errors.New("turned off")
)

// Start marks the scheduler as started now: the quiet period counts from
// here, so nothing runs at startup. Run calls it; tests call it directly.
func (s *AutoScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		s.started, s.lastBusy = true, s.Now()
	}
}

// Run checks every Interval until ctx ends. A run in progress is cancelled
// with ctx; Run returns once it has stopped and its results are stored.
func (s *AutoScheduler) Run(ctx context.Context) {
	s.Start()
	interval := s.Interval
	if interval <= 0 {
		interval = autoDefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Step(ctx)
		}
	}
}

// Step makes one decision and, when everything allows it, runs one
// runtime's stale models. It returns the outcome (one of the AutoCheck
// values, with a reason appended for busy, cancelled and failed).
func (s *AutoScheduler) Step(ctx context.Context) string {
	s.Start()
	now := s.Now()
	cfg, err := LoadAutoConfig(ctx, s.DB)
	if err != nil {
		s.logf("auto re-validation: %v", err)
		s.markBusy(now)
		return AutoCheckFailed
	}
	if !cfg.Enabled {
		// Turning it on later still waits a full quiet period.
		s.markBusy(now)
		return s.record(ctx, now, AutoCheckOff, time.Time{})
	}
	if busy, why := s.Busy(ctx); busy {
		s.markBusy(now)
		return s.record(ctx, now, withReason(AutoCheckBusy, why), time.Time{})
	}
	s.mu.Lock()
	quietUntil, planAfter := s.lastBusy.Add(cfg.QuietPeriod), s.planAfter
	s.mu.Unlock()
	if now.Before(quietUntil) {
		return s.record(ctx, now, AutoCheckQuiet, quietUntil)
	}
	if now.Before(planAfter) {
		return s.record(ctx, now, AutoCheckNothing, planAfter)
	}
	// A state that can't be read must not be taken for "nothing ran
	// today": the cap would fail open, and saving it would reset the count.
	st, err := LoadAutoState(ctx, s.DB, now)
	if err != nil {
		s.logf("auto re-validation: reading state: %v", err)
		s.markBusy(now)
		return AutoCheckFailed
	}
	if st.RuntimesToday >= cfg.MaxRuntimesPerDay {
		return s.record(ctx, now, AutoCheckCap, nextDay(now).Add(cfg.QuietPeriod))
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopWatch := s.watch(runCtx, cancel)
	defer stopWatch()

	plan, err := s.Pick(runCtx, cfg.MaxModelsPerRun)
	if cause := context.Cause(runCtx); cause != nil && ctx.Err() == nil {
		s.markBusy(s.Now())
		return s.record(ctx, now, withReason(AutoCheckCancelled, cause.Error()), time.Time{})
	}
	if err != nil {
		s.logf("auto re-validation: planning: %v", err)
		s.markBusy(now)
		return s.record(ctx, now, withReason(AutoCheckFailed, err.Error()), time.Time{})
	}
	if plan == nil || len(plan.Targets) == 0 {
		backoff := s.NothingBackoff
		if backoff <= 0 {
			backoff = autoDefaultBackoff
		}
		next := now.Add(max(backoff, cfg.QuietPeriod))
		s.mu.Lock()
		s.planAfter = next
		s.mu.Unlock()
		return s.record(ctx, now, AutoCheckNothing, next)
	}
	release, ok := s.Lock()
	if !ok {
		s.markBusy(now)
		return s.record(ctx, now, AutoCheckLocked, time.Time{})
	}
	defer release()

	// The runtime counts against today's cap before the calls are made, so
	// a crash mid-run can never lead to more runs than the cap.
	st.RuntimesToday++
	st.LastRunAt, st.LastRuntime, st.LastSummary, st.LastError = now, plan.Runtime, nil, ""
	if err := SaveAutoState(ctx, s.DB, st); err != nil {
		s.logf("auto re-validation: saving state: %v", err)
		return AutoCheckFailed
	}
	s.logf("auto re-validation: testing %d stale model(s) of %s (≈ $%.4f, %d with unknown cost)",
		len(plan.Targets), plan.Runtime, plan.EstCostUSD, plan.UnknownCost)
	res, runErr := s.Validate(runCtx, *plan)
	stopWatch()
	end := s.Now()
	s.markBusy(end)

	out := AutoCheckRan
	switch cause := context.Cause(runCtx); {
	case cause != nil && ctx.Err() == nil:
		out = withReason(AutoCheckCancelled, cause.Error())
	case runErr != nil:
		out = withReason(AutoCheckFailed, runErr.Error())
	}
	saveCtx := context.WithoutCancel(ctx)
	st, err = LoadAutoState(saveCtx, s.DB, end)
	if err != nil {
		// Never save over a state that couldn't be read: the count would
		// go back to zero.
		s.logf("auto re-validation: reading state after the run: %v", err)
		return out
	}
	sum := res.Summary
	st.LastSummary = &sum
	st.SpentTodayUSD += res.SpentUSD
	st.UnknownCostCall += res.UnknownCost
	if runErr != nil {
		st.LastError = runErr.Error()
	}
	next := end.Add(cfg.QuietPeriod)
	if st.RuntimesToday >= cfg.MaxRuntimesPerDay {
		next = nextDay(end).Add(cfg.QuietPeriod)
	}
	st.LastCheckAt, st.LastCheck, st.NextEligibleAt = end, out, next
	if err := SaveAutoState(saveCtx, s.DB, st); err != nil {
		s.logf("auto re-validation: saving state: %v", err)
	}
	s.logf("auto re-validation: %s: %s: %d ok, %d failed, %d cancelled", plan.Runtime, out, sum.OK, sum.Failed, sum.Cancelled)
	return out
}

// watch cancels a run in progress when the app gets busy or the setting is
// turned off. stop ends the watch and waits for it; it may be called twice.
func (s *AutoScheduler) watch(ctx context.Context, cancel context.CancelCauseFunc) (stop func()) {
	every := s.WatchInterval
	if every <= 0 {
		every = autoDefaultWatch
	}
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if cfg, err := LoadAutoConfig(ctx, s.DB); err == nil && !cfg.Enabled {
				cancel(errAutoTurnedOff)
				return
			}
			if busy, why := s.Busy(ctx); busy {
				cancel(fmt.Errorf("%w (%s)", errAutoBusy, why))
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
}

func withReason(check, why string) string {
	if why == "" {
		return check
	}
	return check + ": " + why
}

func (s *AutoScheduler) markBusy(t time.Time) {
	s.mu.Lock()
	s.lastBusy = t
	s.mu.Unlock()
}

// record stores the latest decision when it changed; a repeated decision
// is not rewritten every minute. next is when a run may start at the
// earliest; zero when that depends on something else ending (busy, locked).
// A state that can't be read is left alone.
func (s *AutoScheduler) record(ctx context.Context, now time.Time, check string, next time.Time) string {
	st, err := LoadAutoState(ctx, s.DB, now)
	if err != nil {
		s.logf("auto re-validation: reading state: %v", err)
		return check
	}
	if st.LastCheck == check && st.NextEligibleAt.Equal(next) {
		return check
	}
	st.LastCheckAt, st.LastCheck, st.NextEligibleAt = now, check, next
	if err := SaveAutoState(ctx, s.DB, st); err != nil {
		s.logf("auto re-validation: saving state: %v", err)
	}
	return check
}

func (s *AutoScheduler) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// nextDay is local midnight after t: the daily cap follows the daemon's
// local time zone.
func nextDay(t time.Time) time.Time {
	l := t.Local()
	return time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, l.Location())
}
