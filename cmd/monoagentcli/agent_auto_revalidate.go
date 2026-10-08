package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/monomind"
)

// Automatic re-validation of stale roster entries (monoes/mono-agent#230).
// The daemon runs the scheduler; `agent roster auto-revalidate` turns it on
// or off and shows its status. Off by default: every check is a real model
// call that costs money.

// autoRevalidateMoneyNote is printed wherever the setting is shown.
const autoRevalidateMoneyNote = "Each re-check is a real, paid model call (a full agent turn); what it costs depends on the model."

// validateLockPath is the lock every validation run takes, so a manual
// `agent validate` and the daemon's automatic one never overlap.
func validateLockPath() string {
	return filepath.Join(filepath.Dir(daemonhb.Path()), "agent-validate.lock")
}

// errValidationRunning is what `agent validate` returns when another
// validation (manual or automatic) holds the lock.
var errValidationRunning = errors.New("another validation is running (a second `agent validate` or the daemon's automatic re-validation); try again when it finishes")

func lockValidation() (release func(), err error) {
	release, err = daemonhb.LockFile(validateLockPath())
	if errors.Is(err, daemonhb.ErrHeld) {
		return nil, errValidationRunning
	}
	return release, err
}

// chatTurnActiveWindow bounds how long an "active" chat turn row counts as
// running without any update: a turn left active by a crash stops blocking
// re-validation after this long.
const chatTurnActiveWindow = 2 * time.Hour

// appBusy reports whether a chat turn, a workflow run or an org run is
// active. Every check is a cheap read: two queries and each profile
// folder's `org serve` heartbeat file.
func appBusy(ctx context.Context, db *sql.DB) (bool, string) {
	var n int
	since := time.Now().Add(-chatTurnActiveWindow).UTC().Format(time.RFC3339)
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_chat_turns WHERE status = 'active' AND updated_at >= ?`, since).Scan(&n); err == nil && n > 0 {
		return true, "chat turn"
	}
	if rows, err := db.QueryContext(ctx, `SELECT COALESCE(pid, 0) FROM workflow_executions WHERE status = 'RUNNING'`); err == nil {
		var pids []int
		for rows.Next() {
			var pid int
			if rows.Scan(&pid) == nil {
				pids = append(pids, pid)
			}
		}
		rows.Close()
		for _, pid := range pids {
			if pid > 0 && (pid == os.Getpid() || daemonhb.ProcessAlive(pid)) {
				return true, "workflow run"
			}
		}
	}
	if roots, err := profileRoots(db); err == nil {
		for _, pr := range roots {
			if hb, live := monomind.ReadServeHeartbeat(pr.Root); live && len(hb.Running) > 0 {
				return true, "org run"
			}
		}
	}
	return false, ""
}

// autoRosterPick builds the roster (with a scan, for version staleness)
// and picks the next runtime's stale models.
func autoRosterPick(ctx context.Context, db *sql.DB, scan *monomind.ScanResult, maxModels int) (*agentroster.AutoPlan, error) {
	results, err := agentroster.List(ctx, db)
	if err != nil {
		return nil, err
	}
	roster := agentroster.Build(results, scan, time.Now(), agentroster.DefaultMaxAge)
	if scan == nil {
		// No scan (status --no-scan): installs are unknown, count them in
		// for the estimate. The caller must not name the runtime.
		for i := range roster {
			roster[i].Installed = true
		}
	}
	return agentroster.PickStale(roster, results, maxModels), nil
}

// newAutoRevalidator is the daemon's scheduler over the real roster,
// monomind and validation lock.
func newAutoRevalidator(db *sql.DB, logf func(string, ...interface{})) *agentroster.AutoScheduler {
	var scan *monomind.ScanResult // the last Pick's scan, reused by Validate
	return &agentroster.AutoScheduler{
		DB:   db,
		Now:  time.Now,
		Busy: func(ctx context.Context) (bool, string) { return busyOrLocked(ctx, db) },
		Pick: func(ctx context.Context, maxModels int) (*agentroster.AutoPlan, error) {
			s, err := monomind.Scan(ctx)
			if err != nil {
				return nil, err
			}
			scan = s
			return autoRosterPick(ctx, db, s, maxModels)
		},
		Repick: func(ctx context.Context, maxModels int) (*agentroster.AutoPlan, error) {
			return autoRosterPick(ctx, db, scan, maxModels)
		},
		Validate: func(ctx context.Context, p agentroster.AutoPlan) (agentroster.AutoRunResult, error) {
			return runAutoRevalidation(ctx, db, scan, p)
		},
		Lock: func() (func(), bool) {
			release, err := lockValidation()
			return release, err == nil
		},
		Logf: func(format string, args ...any) { logf(format, args...) },
	}
}

// runAutoRevalidation tests the plan's models one at a time and stores the
// results as a normal validate run.
func runAutoRevalidation(ctx context.Context, db *sql.DB, scan *monomind.ScanResult, p agentroster.AutoPlan) (agentroster.AutoRunResult, error) {
	var out agentroster.AutoRunResult
	bin, _, err := monomind.Ensure(ctx)
	if err != nil {
		return out, err
	}
	caps, _ := monomind.Capabilities(ctx)
	test := agentroster.AgentTestFunc(caps, bin, agentroster.SandboxModes(scan))
	runID := uuid.NewString()
	if err := agentroster.StartRun(ctx, db, runID, len(p.Targets), time.Now()); err != nil {
		return out, err
	}
	saveCtx := context.WithoutCancel(ctx)
	out.Summary = agentroster.Run(ctx, p.Targets, agentroster.RunOptions{
		RunID: runID, Bin: bin, Timeout: 60 * time.Second, Concurrency: 1, Test: test,
		Save: func(r agentroster.Result) error { return agentroster.Save(saveCtx, db, r) },
	}, out.Add)
	return out, agentroster.FinishRun(saveCtx, db, runID, out.Summary, time.Now())
}

// startAutoRevalidation runs the scheduler until ctx ends; wait blocks
// until it has stopped (a run in progress is cancelled and its finished
// results stored).
func startAutoRevalidation(ctx context.Context, db *sql.DB, logf func(string, ...interface{})) (wait func()) {
	s := newAutoRevalidator(db, logf)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	return func() { <-done }
}

// autoRevalidateStatus is `auto-revalidate status --json`.
type autoRevalidateStatus struct {
	agentroster.AutoConfig
	QuietText     string                `json:"quiet_period"`
	State         agentroster.AutoState `json:"state"`
	DaemonRunning bool                  `json:"daemon_running"`
	Next          *agentroster.AutoPlan `json:"next,omitempty"` // what the next run would test
	// NextUnchecked: installs weren't checked (--no-scan), so next.runtime
	// is left empty rather than naming a runtime that may be gone.
	NextUnchecked bool `json:"next_unchecked,omitempty"`
	// StateError is set when the stored state can't be read; `on` resets it.
	StateError string `json:"state_error,omitempty"`
	// DailyMaxUSD is the most a day can cost: every run at the cap, every
	// model priced like PriciestUSD, the priciest model with a known cost.
	// Both are 0 and CostKnown false before any model reported a cost.
	DailyMaxUSD float64 `json:"daily_max_usd"`
	PriciestUSD float64 `json:"priciest_model_usd"`
	CostKnown   bool    `json:"cost_known"`
	Note        string  `json:"note"`
}

func newAgentRosterAutoCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto-revalidate",
		Short: "Re-check stale roster models in the background (off by default; costs money when on)",
		Long: "When on, the daemon re-validates stale roster models (passed once, but older than 7 days " +
			"or on an older runtime version) in the background: one runtime at a time, only while no chat " +
			"turn, workflow run or org run is active and after a quiet period, never at startup, and at " +
			"most --per-day runtimes a day (the daemon's local day) with at most --max-models models each. Failed and untested " +
			"models are left for you to validate.\n\n" + autoRevalidateMoneyNote + " It is off by default. " +
			"`status` shows the estimated cost of the next run.",
		Example: `  monoagentcli agent roster auto-revalidate status
  monoagentcli agent roster auto-revalidate on
  monoagentcli agent roster auto-revalidate on --per-day 2 --max-models 5 --quiet 30m
  monoagentcli agent roster auto-revalidate off`,
	}
	cmd.AddCommand(newAgentRosterAutoOnCmd(cfg), newAgentRosterAutoOffCmd(cfg), newAgentRosterAutoStatusCmd(cfg))
	return cmd
}

func newAgentRosterAutoOnCmd(cfg *globalConfig) *cobra.Command {
	var perDay, maxModels int
	var quietRaw string
	cmd := &cobra.Command{
		Use:   "on",
		Short: "Turn automatic re-validation on (spends money on model calls)",
		Long: "Turns automatic re-validation on. Limits not given keep their current value (defaults: " +
			fmt.Sprintf("%d runtime a day, %d models per run, %s quiet period). ", agentroster.DefaultAutoRuntimesPerDay,
				agentroster.DefaultAutoModelsPerRun, agentroster.DefaultAutoQuietPeriod) + autoRevalidateMoneyNote,
		RunE: func(cmd *cobra.Command, args []string) error {
			quiet, err := parseDurationFlag(quietRaw)
			if err != nil {
				return err
			}
			if perDay < 0 || perDay > agentroster.MaxAutoRuntimesPerDay {
				return errInvalidInput("--per-day must be 1..%d", agentroster.MaxAutoRuntimesPerDay)
			}
			if maxModels < 0 || maxModels > agentroster.MaxAutoModelsPerRun {
				return errInvalidInput("--max-models must be 1..%d", agentroster.MaxAutoModelsPerRun)
			}
			if quiet != 0 && quiet < agentroster.MinAutoQuietPeriod {
				return errInvalidInput("--quiet must be at least %s", agentroster.MinAutoQuietPeriod)
			}
			return setAutoRevalidate(cmd.Context(), cfg, func(c *agentroster.AutoConfig) {
				c.Enabled = true
				if perDay > 0 {
					c.MaxRuntimesPerDay = perDay
				}
				if maxModels > 0 {
					c.MaxModelsPerRun = maxModels
				}
				if quiet > 0 {
					c.QuietPeriod = quiet
				}
			})
		},
	}
	cmd.Flags().IntVar(&perDay, "per-day", 0, fmt.Sprintf("Runtimes re-checked per day at most (1..%d)", agentroster.MaxAutoRuntimesPerDay))
	cmd.Flags().IntVar(&maxModels, "max-models", 0, fmt.Sprintf("Models re-checked per run at most (1..%d)", agentroster.MaxAutoModelsPerRun))
	cmd.Flags().StringVar(&quietRaw, "quiet", "", "How long nothing must run before a re-check starts (e.g. 15m)")
	return cmd
}

func newAgentRosterAutoOffCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Turn automatic re-validation off",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setAutoRevalidate(cmd.Context(), cfg, func(c *agentroster.AutoConfig) { c.Enabled = false })
		},
	}
}

func setAutoRevalidate(ctx context.Context, cfg *globalConfig, change func(*agentroster.AutoConfig)) error {
	db, err := initDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := applyAutoRevalidate(ctx, db.DB, change); err != nil {
		return err
	}
	return printAutoRevalidateStatus(ctx, cfg, db.DB, false)
}

// applyAutoRevalidate changes the setting. Turning it on resets a corrupt
// stored state: otherwise the daemon would refuse to run for good, and
// nothing else can repair it. A state that can't be read right now (a busy
// database) fails the command instead.
func applyAutoRevalidate(ctx context.Context, db *sql.DB, change func(*agentroster.AutoConfig)) error {
	c, err := agentroster.LoadAutoConfig(ctx, db)
	if err != nil {
		return err
	}
	change(&c)
	if c.Enabled {
		if _, err := agentroster.ResetCorruptAutoState(ctx, db, time.Now()); err != nil {
			return err
		}
	}
	return agentroster.SaveAutoConfig(ctx, db, c)
}

func newAgentRosterAutoStatusCmd(cfg *globalConfig) *cobra.Command {
	var noScan bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the setting, today's runs and spend, and the next run's estimated cost (no model calls)",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return err
			}
			defer db.Close()
			return printAutoRevalidateStatus(cmd.Context(), cfg, db.DB, !noScan)
		},
	}
	cmd.Flags().BoolVar(&noScan, "no-scan", false, "Skip `agent scan` (the next-run estimate then misses version staleness)")
	return cmd
}

// autoRevalidateSnapshot gathers `status`. An unreadable state is
// reported in StateError, not returned as an error.
func autoRevalidateSnapshot(ctx context.Context, db *sql.DB, scan bool) (autoRevalidateStatus, error) {
	c, err := agentroster.LoadAutoConfig(ctx, db)
	if err != nil {
		return autoRevalidateStatus{}, err
	}
	out := autoRevalidateStatus{AutoConfig: c, QuietText: c.QuietPeriod.String(), Note: autoRevalidateMoneyNote}
	if out.State, err = agentroster.LoadAutoState(ctx, db, time.Now()); err != nil {
		out.State, out.StateError = agentroster.AutoState{}, err.Error()
	}
	var sr *monomind.ScanResult
	if scan {
		sr, _ = monomind.Scan(ctx) // without a scan the estimate still covers age staleness
	}
	out.Next, _ = autoRosterPick(ctx, db, sr, c.MaxModelsPerRun)
	if out.Next != nil && sr == nil {
		out.Next.Runtime, out.NextUnchecked = "", true
		for i := range out.Next.Targets {
			out.Next.Targets[i].Runtime = ""
		}
	}
	results, err := agentroster.List(ctx, db)
	if err != nil {
		return out, err
	}
	out.DailyMaxUSD, out.PriciestUSD, out.CostKnown = agentroster.DailyCeiling(c, results)
	hb, ok := daemonhb.Read()
	out.DaemonRunning = ok && daemonhb.IsLive(hb, time.Now())
	return out, nil
}

func printAutoRevalidateStatus(ctx context.Context, cfg *globalConfig, db *sql.DB, scan bool) error {
	out, err := autoRevalidateSnapshot(ctx, db, scan)
	if err != nil {
		return err
	}
	c, st, next := out.AutoConfig, out.State, out.Next
	perDay, priciest, known := out.DailyMaxUSD, out.PriciestUSD, out.CostKnown
	if cfg.JSONOutput {
		return printJSON(out)
	}
	state := "off"
	if c.Enabled {
		state = "on"
	}
	fmt.Printf("Automatic re-validation: %s\n", state)
	fmt.Printf("Limits: %d runtime(s) a day, %d model(s) per run, after %s with nothing running\n",
		c.MaxRuntimesPerDay, c.MaxModelsPerRun, c.QuietPeriod)
	if known {
		fmt.Printf("Daily ceiling: up to %d run(s) × %d model(s), %s/day at the priciest cost seen so far (%s a model); models with unknown cost not included\n",
			c.MaxRuntimesPerDay, c.MaxModelsPerRun, usd(perDay, 0), usd(priciest, 0))
	} else {
		fmt.Printf("Daily ceiling: up to %d run(s) × %d model(s); no model has reported a cost yet\n", c.MaxRuntimesPerDay, c.MaxModelsPerRun)
	}
	if out.StateError != "" {
		fmt.Printf("Today: state unreadable (%s); runs are stopped until `auto-revalidate on` resets it\n", out.StateError)
	} else {
		fmt.Printf("Today: %d run(s), spent %s\n", st.RuntimesToday, usd(st.SpentTodayUSD, st.UnknownCostCall))
	}
	if !st.LastRunAt.IsZero() {
		line := fmt.Sprintf("Last run: %s, %s", st.LastRunAt.Local().Format("2006-01-02 15:04"), st.LastRuntime)
		if s := st.LastSummary; s != nil {
			line += fmt.Sprintf(" (%d ok, %d failed, %d cancelled)", s.OK, s.Failed, s.Cancelled)
		}
		if st.LastError != "" {
			line += ": " + st.LastError
		}
		fmt.Println(line)
	}
	if c.Enabled {
		if !out.DaemonRunning {
			fmt.Println("The daemon isn't running, so nothing runs until it starts (`monoagentcli daemon`).")
		} else if st.LastCheck != "" {
			line := "Last check: " + st.LastCheck
			if !st.NextEligibleAt.IsZero() {
				line += "; next run at " + st.NextEligibleAt.Local().Format("2006-01-02 15:04") + " at the earliest"
			}
			fmt.Println(line)
		}
	}
	var cost string
	if next != nil {
		cost = usd(next.EstCostUSD, next.UnknownCost)
		if next.TableEstimated > 0 {
			cost += fmt.Sprintf(" (%d priced from the built-in table)", next.TableEstimated)
		}
	}
	if next != nil && out.NextUnchecked {
		fmt.Printf("Next run would test up to %d stale model(s): %s per run (installs not checked; run without --no-scan to see the runtime)\n",
			len(next.Targets), cost)
	} else if next != nil {
		fmt.Printf("Next run would test %d stale model(s) of %s: %s per run, up to %d run(s) a day\n",
			len(next.Targets), next.Runtime, cost, c.MaxRuntimesPerDay)
	} else {
		fmt.Println("Nothing is stale right now.")
	}
	fmt.Println(autoRevalidateMoneyNote)
	return nil
}

func usd(v float64, unknown int) string {
	s := fmt.Sprintf("≈ $%.4f", v)
	if v > 0 && v < 0.0001 {
		s = "≈ <$0.0001"
	}
	if unknown > 0 {
		s += fmt.Sprintf(" (+ %d with unknown cost)", unknown)
	}
	return s
}
