package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/health"
	"github.com/monoes/mono-agent/internal/nodemgr"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/monoes/mono-agent/internal/shellpath"
	"github.com/monoes/mono-agent/internal/storage"
)

// maxFixPasses bounds `doctor --fix`'s fix → re-check loop.
const maxFixPasses = 4

// errRequiredChecksFailed maps a doctor run with failed required checks to
// exit code 1 without an {"error"} JSON line on stdout (the report already
// is the JSON output).
func errRequiredChecksFailed(n int) error {
	return &cliError{code: 1, msg: fmt.Sprintf("%d required check(s) failed", n)}
}

func newDoctorCmd(cfg *globalConfig) *cobra.Command {
	var groups, ids []string
	var deep, fix, yes bool

	cmd := &cobra.Command{
		// Replaces the root's pre-run (Claude first-run setup), which
		// creates ~/.monoagent and copies skills into ~/.claude: a check
		// must not change anything, and the data-folder check could never
		// see the folder missing. `doctor fix` inherits this one.
		PersistentPreRun: func(*cobra.Command, []string) {},
		Use:              "doctor",
		Short:            "Check (and fix) everything monoagent needs on this machine",
		Long: `Runs health checks over every component monoagent depends on and
reports what is missing or broken, with a fix for each problem it can repair.

Fixes have a safety level: "auto" fixes are local and safe to repeat,
"confirm" fixes install software or change configuration and are asked about
first (--yes accepts them), "manual" fixes only print what to do.

Exit code 1 means a required check failed.`,
		Example: `  monoagentcli doctor
  monoagentcli doctor --json
  monoagentcli doctor --group core --deep
  monoagentcli doctor --fix --yes
  monoagentcli doctor fix core.db.migrate --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			reg := health.Default()
			opts := health.Options{Deep: deep, Groups: groups, IDs: ids}
			out := cmd.OutOrStdout()

			env, closeEnv := newHealthEnv(cfg)
			rep := reg.Run(ctx, env, opts)
			closeEnv()

			var outcomes []fixOutcome
			if fix {
				// A fix can unblock checks that were skipped (e.g. the
				// database appears, then the profile folder is checked), so
				// fix → re-check until a pass finds nothing new to do.
				confirm := fixPrompter(cmd, yes, cfg.JSONOutput)
				progress := progressWriter(out, cfg.JSONOutput)
				tried := map[string]bool{}
				for pass := 0; pass < maxFixPasses; pass++ {
					got := applyReportFixes(ctx, cfg, reg, rep, tried, confirm, progress)
					outcomes = append(outcomes, got...)
					env, closeEnv = newHealthEnv(cfg)
					rep = reg.Run(ctx, env, opts)
					closeEnv()
					if len(got) == 0 {
						break
					}
				}
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(doctorJSON{Report: rep, Fixes: outcomes}); err != nil {
					return err
				}
			} else {
				printDoctorReport(out, rep, fix)
			}
			if n := rep.RequiredFailures(); n > 0 {
				return errRequiredChecksFailed(n)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&groups, "group", nil, "Only run checks of these groups (e.g. core)")
	cmd.Flags().StringSliceVar(&ids, "check", nil, "Only run these checks (and what they depend on)")
	cmd.Flags().BoolVar(&deep, "deep", false, "Include checks that use the network")
	cmd.Flags().BoolVar(&fix, "fix", false, "Apply fixes: auto ones directly, confirm ones after asking")
	cmd.Flags().BoolVar(&yes, "yes", false, "With --fix: accept confirm fixes without asking")
	cmd.AddCommand(newDoctorFixCmd(cfg))
	return cmd
}

func newDoctorFixCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "fix <fix-id>",
		Short: "Apply one fix by id (as listed by doctor)",
		Long: `Applies a single fix. With --json, progress is streamed as NDJSON events:
{"kind":"line","message":…} for output, then {"kind":"done"} or
{"kind":"error","message":…}.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg := health.Default()
			f, ok := reg.Fix(args[0])
			if !ok {
				return errNotFound("unknown fix %q", args[0])
			}
			out := cmd.OutOrStdout()
			err := applyFix(cmd.Context(), cfg, f, progressWriter(out, cfg.JSONOutput))
			return finishStreamed(out, cfg.JSONOutput, err, f.Label)
		},
	}
}

// doctorJSON is the `doctor --json` payload: the report plus, with --fix,
// what happened to each fix.
type doctorJSON struct {
	*health.Report
	Fixes []fixOutcome `json:"fixes,omitempty"`
}

type fixOutcome struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"` // applied | failed | declined | manual
	Error   string `json:"error,omitempty"`
}

type fixEvent struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
}

func writeFixEvent(w io.Writer, ev fixEvent) {
	b, _ := json.Marshal(ev)
	fmt.Fprintln(w, string(b))
}

// progressWriter streams fix output: NDJSON line events in JSON mode (to
// stderr for `doctor --fix --json`, whose stdout is the report — see
// applyReportFixes), indented text otherwise.
func progressWriter(w io.Writer, asJSON bool) func(string) {
	if asJSON {
		return func(line string) { writeFixEvent(w, fixEvent{Kind: "line", Message: line}) }
	}
	return func(line string) { fmt.Fprintf(w, "    %s\n", line) }
}

// fixPrompter returns the consent function for confirm fixes.
func fixPrompter(cmd *cobra.Command, yes, asJSON bool) func(health.FixInfo) bool {
	if yes {
		return func(health.FixInfo) bool { return true }
	}
	if asJSON || !stdinIsTerminal() {
		return func(health.FixInfo) bool { return false }
	}
	in := bufio.NewReader(cmd.InOrStdin())
	return func(f health.FixInfo) bool {
		prompt := f.Label
		if f.Command != "" {
			prompt += " (runs: " + f.Command + ")"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  %s? [y/N] ", prompt)
		ans, _ := in.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		return ans == "y" || ans == "yes"
	}
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// applyReportFixes applies every distinct fix the report offers, in report
// order: auto directly, confirm when confirm() agrees, manual never.
func applyReportFixes(ctx context.Context, cfg *globalConfig, reg *health.Registry, rep *health.Report,
	tried map[string]bool, confirm func(health.FixInfo) bool, progress func(string)) []fixOutcome {
	if cfg.JSONOutput {
		// stdout carries the final report; keep progress off it.
		progress = progressWriter(os.Stderr, true)
	}
	var outcomes []fixOutcome
	for _, res := range rep.Results {
		if res.Fix == nil || tried[res.Fix.ID] {
			continue
		}
		tried[res.Fix.ID] = true
		f, ok := reg.Fix(res.Fix.ID)
		if !ok {
			continue
		}
		switch f.Safety {
		case health.SafetyManual:
			outcomes = append(outcomes, fixOutcome{ID: f.ID, Outcome: "manual"})
			continue
		case health.SafetyConfirm:
			if !confirm(f.FixInfo) {
				outcomes = append(outcomes, fixOutcome{ID: f.ID, Outcome: "declined"})
				continue
			}
		}
		if !cfg.JSONOutput {
			progress("→ " + f.Label)
		}
		if err := applyFix(ctx, cfg, f, progress); err != nil {
			outcomes = append(outcomes, fixOutcome{ID: f.ID, Outcome: "failed", Error: err.Error()})
			if !cfg.JSONOutput {
				progress("✗ " + err.Error())
			}
			continue
		}
		outcomes = append(outcomes, fixOutcome{ID: f.ID, Outcome: "applied"})
	}
	return outcomes
}

// applyFix runs one fix against a fresh environment.
func applyFix(ctx context.Context, cfg *globalConfig, f health.Fix, progress func(string)) error {
	if f.Safety == health.SafetyManual {
		msg := "this needs to be done by hand"
		if f.Command != "" {
			msg += ": " + f.Command
		}
		return fmt.Errorf("%s", msg)
	}
	env, closeEnv := newHealthEnv(cfg)
	defer closeEnv()
	return f.Apply(ctx, env, progress)
}

// newHealthEnv builds the real-machine environment for checks and fixes.
// The database is opened only if it already exists and is never migrated
// here — checks must not change anything; the migrate fix does that.
func newHealthEnv(cfg *globalConfig) (*health.Env, func()) {
	home, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	dbPath := expandPath(cfg.DBPath)
	env := &health.Env{
		Home:       home,
		DataDir:    filepath.Join(home, ".monoagent"),
		DBPath:     dbPath,
		Version:    getVersion(),
		Executable: exe,
		ProfileID:  cfg.ProfileID,
		LoginPath:  shellpath.LoginPath,
		FreeBytes:  health.FreeBytes,
		LatestVersion: func(ctx context.Context) (string, error) {
			rel, err := fetchLatestRelease(ctx)
			if err != nil {
				return "", err
			}
			return rel.TagName, nil
		},
		Migrate: func(_ context.Context, progress func(string)) error {
			// The migration runner reports through the standard logger;
			// route it into the fix's progress stream for the duration.
			restore := captureStdLog(progress)
			defer restore()
			db, err := initDB(&globalConfig{DBPath: cfg.DBPath, ProfileID: cfg.ProfileID})
			if err != nil {
				return err
			}
			return db.Close()
		},
	}
	nm := nodemgr.New()
	env.SystemNode = nm.SystemNode
	env.ManagedNode = func() (string, string, bool) {
		v, ok := nm.Current()
		if !ok {
			return "", "", false
		}
		return v, nm.NodePath(v), true
	}
	env.InstallNode = func(ctx context.Context, progress func(string)) error {
		_, err := nm.Install(ctx, "lts", progress)
		return err
	}
	env.ProfileRoot = func(id string) string { return profiledir.Root(env.DB, id) }
	env.EnsureProfile = func(id string) error { return profiledir.EnsureLayout(env.DB, id) }

	closeFn := func() {}
	if _, err := os.Stat(dbPath); err == nil {
		db, err := storage.NewDatabase(dbPath)
		if err != nil {
			env.DBErr = err
		} else {
			env.DB = db.DB
			env.PendingMigrations = db.PendingMigrations
			env.QuickCheck = db.QuickCheck
			env.VaultState = func(ctx context.Context, id string) (string, error) {
				st, err := secrets.CheckVault(ctx, db.DB, id)
				return string(st), err
			}
			closeFn = func() { db.Close() }
			if env.ProfileID == "" {
				var id string
				_ = db.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, profiledir.ActiveProfileSetting).Scan(&id)
				env.ProfileID = id
			} else if id, err := resolveProfileID(db.DB, env.ProfileID); err == nil {
				// --profile takes a name or an id, as it does for every other
				// command; one that matches neither is left for the profile
				// check to report.
				env.ProfileID = id
			}
		}
	} else if !os.IsNotExist(err) {
		env.DBErr = err
	}
	if env.ProfileID == "" {
		env.ProfileID = "default"
	}
	return env, closeFn
}

var statusMark = map[health.Status]string{
	health.StatusOK: "✓", health.StatusWarn: "⚠", health.StatusFail: "✗",
	health.StatusSkip: "–", health.StatusInfo: "ℹ",
}

func printDoctorReport(w io.Writer, rep *health.Report, fixed bool) {
	fmt.Fprintf(w, "monoagent doctor — %s · profile %s\n", rep.MonoagentVersion, rep.ProfileID)
	group := ""
	fixable := 0
	for _, r := range rep.Results {
		if r.Group != group {
			group = r.Group
			fmt.Fprintf(w, "\n%s\n", group)
		}
		req := ""
		if r.Required && r.Status == health.StatusFail {
			req = " (required)"
		}
		fmt.Fprintf(w, "  %s %-18s %s%s\n", statusMark[r.Status], r.Title, r.Summary, req)
		if r.Detail != "" && (r.Status == health.StatusFail || r.Status == health.StatusWarn) {
			for _, line := range strings.Split(r.Detail, "\n") {
				fmt.Fprintf(w, "      %s\n", line)
			}
		}
		if len(r.Features) > 0 && (r.Status == health.StatusFail || r.Status == health.StatusWarn) {
			fmt.Fprintf(w, "      affects: %s\n", strings.Join(r.Features, ", "))
		}
		if r.Fix != nil {
			fixable++
			line := fmt.Sprintf("fix [%s]: %s — monoagentcli doctor fix %s", r.Fix.Safety, r.Fix.Label, r.Fix.ID)
			if r.Fix.Safety == health.SafetyManual && r.Fix.Command != "" {
				line = "to fix: " + r.Fix.Command
			}
			fmt.Fprintf(w, "      %s\n", line)
		}
	}
	s := rep.Summary
	fmt.Fprintf(w, "\n%d ok · %d warning · %d failed · %d skipped · %d info\n",
		s[health.StatusOK], s[health.StatusWarn], s[health.StatusFail], s[health.StatusSkip], s[health.StatusInfo])
	if fixable > 0 && !fixed {
		fmt.Fprintf(w, "Run `monoagentcli doctor --fix` to repair what can be repaired.\n")
	}
}

// captureStdLog sends the standard logger's output to progress, one call
// per line, until the returned restore func is called.
func captureStdLog(progress func(string)) (restore func()) {
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetFlags(0)
	log.SetOutput(lineFunc(progress))
	return func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}
}

// lineFunc is an io.Writer calling f for each line written (the standard
// logger writes whole lines).
type lineFunc func(string)

func (f lineFunc) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		f(line)
	}
	return len(p), nil
}
