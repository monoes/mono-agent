package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/dynorg"
	"github.com/monoes/mono-agent/internal/monomind"
)

// coderConfigName is the configs-table row holding coder mode's settings.
const coderConfigName = "coder_mode"

// coderSettings is coder mode's persisted configuration. Enabled gates every
// coder conversation and turn: the CLI enforces it, not the app.
type coderSettings struct {
	Enabled       bool    `json:"enabled"`
	WorkspaceRoot string  `json:"workspaceRoot"`
	MaxTurns      int     `json:"maxTurns"`
	Timeout       string  `json:"timeout"`
	BudgetUSD     float64 `json:"budgetUsd"`
	// Dynamic org (#226): per-turn worker limits, the workers' budget
	// (0 = none), and who picks a worker's model when the lead doesn't
	// ("lead-then-jev" or "lead").
	OrgMaxAgents     int     `json:"orgMaxAgents"`
	OrgMaxConcurrent int     `json:"orgMaxConcurrent"`
	OrgBudgetUSD     float64 `json:"orgBudgetUsd"`
	OrgModelPicker   string  `json:"orgModelPicker"`
	// OrgWriters is "isolated" to give each writing worker its own git
	// worktree and branch, merged by the lead (#230), or "shared" (the
	// default): writers take turns under the write lease.
	OrgWriters string `json:"orgWriters"`
}

const (
	defaultCoderMaxTurns = 200
	defaultCoderTimeout  = "60m"
)

// withDefaults fills unset fields.
func (s coderSettings) withDefaults() coderSettings {
	if s.WorkspaceRoot == "" {
		if home, err := os.UserHomeDir(); err == nil {
			s.WorkspaceRoot = filepath.Join(home, "monoagent-coder")
		}
	}
	if s.MaxTurns <= 0 {
		s.MaxTurns = defaultCoderMaxTurns
	}
	if s.Timeout == "" {
		s.Timeout = defaultCoderTimeout
	}
	d := dynorg.DefaultLimits()
	if s.OrgMaxAgents <= 0 {
		s.OrgMaxAgents = d.MaxAgents
	}
	if s.OrgMaxConcurrent <= 0 {
		s.OrgMaxConcurrent = d.MaxConcurrent
	}
	if s.OrgModelPicker != dynorg.PickerLead {
		s.OrgModelPicker = dynorg.PickerLeadThenJev
	}
	if s.OrgWriters != dynorg.WritersIsolated {
		s.OrgWriters = dynorg.WritersShared
	}
	return s
}

func loadCoderSettings(db *sql.DB) (coderSettings, error) {
	var raw string
	err := db.QueryRow(`SELECT config_data FROM configs WHERE name = ?`, coderConfigName).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return coderSettings{}.withDefaults(), nil
	}
	if err != nil {
		return coderSettings{}, fmt.Errorf("reading coder settings: %w", err)
	}
	var s coderSettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return coderSettings{}, fmt.Errorf("reading coder settings: %w", err)
	}
	return s.withDefaults(), nil
}

func saveCoderSettings(db *sql.DB, s coderSettings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO configs (name, config_data, created_at, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET config_data = excluded.config_data, updated_at = excluded.updated_at`,
		coderConfigName, string(b))
	if err != nil {
		return fmt.Errorf("saving coder settings: %w", err)
	}
	return nil
}

// coderStatus is `coder status --json`. Ready and MissingCapabilities are
// monomind's global coder support; Runtimes is each runtime's, and Runtime
// stays "claude" for app versions that only know that one.
type coderStatus struct {
	coderSettings
	Ready               bool                    `json:"ready"`
	MissingCapabilities []string                `json:"missingCapabilities"`
	MonomindVersion     string                  `json:"monomindVersion"`
	Runtime             string                  `json:"runtime"`
	Runtimes            []monomind.CoderRuntime `json:"runtimes"`

	caps *monomind.CapabilitySet
	scan *monomind.ScanResult // nil when the scan failed
}

// capabilityProbe is monomind's capability handshake; a var so tests can
// stand in for a monomind that has (or lacks) the coder capabilities.
var capabilityProbe = func(cmd *cobra.Command) (*monomind.CapabilitySet, error) {
	return monomind.Capabilities(cmd.Context())
}

// runtimeScan is `monomind agent scan`; a var so tests can stand in for
// the installed runtimes.
var runtimeScan = monomind.Scan

func currentCoderStatus(cmd *cobra.Command, s coderSettings) coderStatus {
	st := coderStatus{coderSettings: s, Runtime: monomind.DefaultCoderRuntime}
	set, err := capabilityProbe(cmd)
	if err == nil && set != nil {
		st.MonomindVersion = set.Version
		st.caps = set
	}
	st.MissingCapabilities = monomind.MissingCoderCapabilities(set)
	if st.MissingCapabilities == nil {
		st.MissingCapabilities = []string{}
	}
	st.Ready = len(st.MissingCapabilities) == 0
	var scan *monomind.ScanResult
	if err == nil {
		// A failed scan leaves claude alone, as before per-runtime support.
		scan, _ = runtimeScan(cmd.Context())
	}
	st.scan = scan
	st.Runtimes = monomind.CoderRuntimes(scan, st.caps)
	return st
}

// coderError is a coder-mode refusal with a machine-readable code for --json.
type coderError struct {
	code string
	err  *cliError
}

func (e *coderError) Error() string                   { return e.err.Error() }
func (e *coderError) Unwrap() error                   { return e.err }
func (e *coderError) JSONErrorFields() map[string]any { return map[string]any{"code": e.code} }

func errCoderDisabled() error {
	return &coderError{code: "coder_disabled", err: &cliError{code: 3,
		msg: "coder mode is disabled — enable it in Settings or with `monoagentcli coder enable --yes-i-understand`"}}
}

func errNeedsMonomindUpdate(missing []string) error {
	return &coderError{code: "needs_monomind_update", err: &cliError{code: 1,
		msg: fmt.Sprintf("coder mode needs a newer monomind (missing capabilities: %v) — update monomind: `npm install -g @monoes/monomindcli@latest`", missing)}}
}

// errCoderRuntime refuses a runtime monomind won't run with full access:
// unknown to it, left out by an older monomind (claude only), or not a
// coding agent at all.
func errCoderRuntime(runtime string, known, olderMonomind bool) error {
	msg := fmt.Sprintf("coder mode can't run on %s: monomind doesn't run it with full access (see `monoagentcli coder status`)", runtime)
	switch {
	case !known:
		msg = fmt.Sprintf("coder mode can't run on %q: monomind doesn't know that runtime (see `monoagentcli coder status`)", runtime)
	case olderMonomind:
		msg = fmt.Sprintf("coder mode runs on %s only with a newer monomind (the installed one runs claude only) — update monomind: `npm install -g @monoes/monomindcli@latest`", runtime)
	}
	return &coderError{code: "coder_runtime_unsupported", err: &cliError{code: 3, msg: msg}}
}

// requireCoderReady refuses unless coder mode is enabled, monomind supports
// it and runs runtime ("" = claude) with full access, and this process is
// not root. An uninstalled runtime is not refused here: the turn itself
// reports it as not set up, which the app knows how to offer to fix.
func requireCoderReady(cmd *cobra.Command, db *sql.DB, runtime string) (coderSettings, coderStatus, monomind.CoderRuntime, error) {
	s, err := loadCoderSettings(db)
	if err != nil {
		return s, coderStatus{}, monomind.CoderRuntime{}, err
	}
	if !s.Enabled {
		return s, coderStatus{}, monomind.CoderRuntime{}, errCoderDisabled()
	}
	if monomind.IsRoot() {
		return s, coderStatus{}, monomind.CoderRuntime{}, errInvalidInput("coder mode refuses to run as root")
	}
	st := currentCoderStatus(cmd, s)
	if !st.Ready {
		return s, st, monomind.CoderRuntime{}, errNeedsMonomindUpdate(st.MissingCapabilities)
	}
	if runtime == "" {
		runtime = monomind.DefaultCoderRuntime
	}
	r := monomind.FindCoderRuntime(st.Runtimes, runtime)
	if r == nil || !r.FullAccess {
		return s, st, monomind.CoderRuntime{}, errCoderRuntime(runtime, r != nil, !st.caps.Has(monomind.CapAgentExecFullAccessAny))
	}
	return s, st, *r, nil
}

func newCoderCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coder",
		Short: "Coder mode: chats where the agent has full access to this computer",
		Long: "Coder mode runs a chat as a full coding-agent session (Claude Code, Codex, OpenCode, … — " +
			"see `coder status` for which runtimes are ready) inside a folder: it can run any command and " +
			"read or change any file your user can, with no approval prompts, and it loads your normal " +
			"setup for that agent (its instructions files, skills, hooks, MCP servers). It is off until " +
			"enabled here. Web pages and files in the folder can carry instructions that steer it, so " +
			"only point it at folders you trust.",
	}
	cmd.AddCommand(newCoderStatusCmd(cfg), newCoderEnableCmd(cfg), newCoderDisableCmd(cfg), newCoderSetCmd(cfg), newCoderWorkspaceCmd(cfg), newCoderStopBackgroundCmd(cfg))
	return cmd
}

// coderSettingsCmd builds a subcommand that optionally changes the settings
// and then prints the status.
func coderSettingsCmd(cfg *globalConfig, use, short string, change func(cmd *cobra.Command, s *coderSettings) error) *cobra.Command {
	c := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()
			s, err := loadCoderSettings(db.DB)
			if err != nil {
				return err
			}
			if change != nil {
				if err := change(cmd, &s); err != nil {
					return err
				}
				if err := saveCoderSettings(db.DB, s); err != nil {
					return err
				}
			}
			st := currentCoderStatus(cmd, s)
			if cfg.JSONOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(st)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "enabled: %v\nworkspace root: %s\nmax turns: %d\ntimeout: %s\nbudget: $%g\nready: %v\n",
				st.Enabled, st.WorkspaceRoot, st.MaxTurns, st.Timeout, st.BudgetUSD, st.Ready)
			if !st.Ready {
				fmt.Fprintf(cmd.OutOrStdout(), "missing monomind capabilities: %v\n", st.MissingCapabilities)
			}
			for _, r := range st.Runtimes {
				if r.FullAccess {
					fmt.Fprintf(cmd.OutOrStdout(), "runtime %s: installed %v, ready %v, tool activity %s\n", r.ID, r.Installed, r.Ready, r.ToolActivity)
				}
			}
			return nil
		},
	}
	withJSONErrors(cfg, c)
	return c
}

func newCoderStatusCmd(cfg *globalConfig) *cobra.Command {
	return coderSettingsCmd(cfg, "status", "Show coder mode's settings and whether monomind supports it", nil)
}

func newCoderEnableCmd(cfg *globalConfig) *cobra.Command {
	var yes bool
	c := coderSettingsCmd(cfg, "enable", "Enable coder mode (full-access chats)", func(_ *cobra.Command, s *coderSettings) error {
		if !yes {
			return errInvalidInput("coder mode gives the agent full access to this computer; pass --yes-i-understand to enable it")
		}
		s.Enabled = true
		return nil
	})
	c.Flags().BoolVar(&yes, "yes-i-understand", false, "Confirm that coder chats can run any command and change any file without asking")
	return c
}

func newCoderDisableCmd(cfg *globalConfig) *cobra.Command {
	return coderSettingsCmd(cfg, "disable", "Disable coder mode", func(_ *cobra.Command, s *coderSettings) error {
		s.Enabled = false
		return nil
	})
}

func newCoderSetCmd(cfg *globalConfig) *cobra.Command {
	var (
		root     string
		maxTurns int
		timeout  string
		budget   float64
		orgMax   int
		orgConc  int
		orgBudg  float64
		picker   string
		writers  string
	)
	c := coderSettingsCmd(cfg, "set", "Change coder mode's defaults", func(cmd *cobra.Command, s *coderSettings) error {
		f := cmd.Flags()
		if f.Changed("workspace-root") {
			abs, err := filepath.Abs(expandHome(root))
			if err != nil {
				return errInvalidInput("--workspace-root: %v", err)
			}
			s.WorkspaceRoot = abs
		}
		if f.Changed("max-turns") {
			if maxTurns <= 0 {
				return errInvalidInput("--max-turns must be positive")
			}
			s.MaxTurns = maxTurns
		}
		if f.Changed("timeout") {
			d, err := parseDurationFlag(timeout)
			if err != nil || d <= 0 {
				return errInvalidInput("--timeout must be a positive duration (e.g. 30m, 2h)")
			}
			s.Timeout = d.String()
		}
		if f.Changed("budget-usd") {
			if budget < 0 {
				return errInvalidInput("--budget-usd can't be negative (0 means no cap)")
			}
			s.BudgetUSD = budget
		}
		if f.Changed("org-max-agents") {
			if orgMax <= 0 || orgMax > 20 {
				return errInvalidInput("--org-max-agents must be 1-20")
			}
			s.OrgMaxAgents = orgMax
		}
		if f.Changed("org-max-concurrent") {
			if orgConc <= 0 || orgConc > 10 {
				return errInvalidInput("--org-max-concurrent must be 1-10")
			}
			s.OrgMaxConcurrent = orgConc
		}
		if f.Changed("org-budget-usd") {
			if orgBudg < 0 {
				return errInvalidInput("--org-budget-usd can't be negative (0 means no cap)")
			}
			s.OrgBudgetUSD = orgBudg
		}
		if f.Changed("org-model-picker") {
			if picker != dynorg.PickerLead && picker != dynorg.PickerLeadThenJev {
				return errInvalidInput("--org-model-picker must be %q or %q", dynorg.PickerLeadThenJev, dynorg.PickerLead)
			}
			s.OrgModelPicker = picker
		}
		if f.Changed("org-writers") {
			if writers != dynorg.WritersShared && writers != dynorg.WritersIsolated {
				return errInvalidInput("--org-writers must be %q or %q", dynorg.WritersShared, dynorg.WritersIsolated)
			}
			s.OrgWriters = writers
		}
		return nil
	})
	c.Flags().StringVar(&root, "workspace-root", "", "Folder new test workspaces are created in")
	c.Flags().IntVar(&maxTurns, "max-turns", 0, "Agent turn cap per message")
	c.Flags().StringVar(&timeout, "timeout", "", "Wall-clock cap per message (e.g. 60m)")
	c.Flags().Float64Var(&budget, "budget-usd", 0, "Spend cap per message in USD (0 = none)")
	c.Flags().IntVar(&orgMax, "org-max-agents", 0, "Dynamic org: workers the lead may spawn per message (default 6)")
	c.Flags().IntVar(&orgConc, "org-max-concurrent", 0, "Dynamic org: workers running at once (default 3)")
	c.Flags().Float64Var(&orgBudg, "org-budget-usd", 0, "Dynamic org: reported worker cost cap per message in USD (0 = none)")
	c.Flags().StringVar(&picker, "org-model-picker", "", "Dynamic org: who picks a worker's model when the lead doesn't: lead-then-jev (default) or lead")
	c.Flags().StringVar(&writers, "org-writers", "", "Dynamic org: shared (default; one writer at a time under the write lease) or isolated (each writer gets its own git worktree and branch, merged by the lead)")
	return c
}

// coderTimeout parses the stored timeout, falling back to the default.
func (s coderSettings) timeout() time.Duration {
	if d, err := parseDurationFlag(s.Timeout); err == nil && d > 0 {
		return d
	}
	d, _ := time.ParseDuration(defaultCoderTimeout)
	return d
}

// expandHome expands a leading "~/".
func expandHome(p string) string {
	if p == "~" || len(p) > 1 && p[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
