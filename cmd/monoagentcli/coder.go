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

// coderStatus is `coder status --json`.
type coderStatus struct {
	coderSettings
	Ready               bool     `json:"ready"`
	MissingCapabilities []string `json:"missingCapabilities"`
	MonomindVersion     string   `json:"monomindVersion"`
	Runtime             string   `json:"runtime"`
}

// coderRuntime is the only runtime with full access (monomind#355).
const coderRuntime = "claude"

// capabilityProbe is monomind's capability handshake; a var so tests can
// stand in for a monomind that has (or lacks) the coder capabilities.
var capabilityProbe = func(cmd *cobra.Command) (*monomind.CapabilitySet, error) {
	return monomind.Capabilities(cmd.Context())
}

func currentCoderStatus(cmd *cobra.Command, s coderSettings) coderStatus {
	st := coderStatus{coderSettings: s, Runtime: coderRuntime}
	set, err := capabilityProbe(cmd)
	if err == nil && set != nil {
		st.MonomindVersion = set.Version
	}
	st.MissingCapabilities = monomind.MissingCoderCapabilities(set)
	if st.MissingCapabilities == nil {
		st.MissingCapabilities = []string{}
	}
	st.Ready = len(st.MissingCapabilities) == 0
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

// requireCoderReady refuses unless coder mode is enabled, monomind supports
// it, and this process is not root.
func requireCoderReady(cmd *cobra.Command, db *sql.DB) (coderSettings, error) {
	s, err := loadCoderSettings(db)
	if err != nil {
		return s, err
	}
	if !s.Enabled {
		return s, errCoderDisabled()
	}
	if monomind.IsRoot() {
		return s, errInvalidInput("coder mode refuses to run as root")
	}
	if st := currentCoderStatus(cmd, s); !st.Ready {
		return s, errNeedsMonomindUpdate(st.MissingCapabilities)
	}
	return s, nil
}

func newCoderCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "coder",
		Short: "Coder mode: chats where the agent has full access to this computer",
		Long: "Coder mode runs a chat as a full Claude Code session inside a folder: it can run any " +
			"command and read or change any file your user can, with no approval prompts, and it loads " +
			"your normal Claude Code setup (CLAUDE.md, skills, hooks, MCP servers). It is off until " +
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
		return nil
	})
	c.Flags().StringVar(&root, "workspace-root", "", "Folder new test workspaces are created in")
	c.Flags().IntVar(&maxTurns, "max-turns", 0, "Agent turn cap per message")
	c.Flags().StringVar(&timeout, "timeout", "", "Wall-clock cap per message (e.g. 60m)")
	c.Flags().Float64Var(&budget, "budget-usd", 0, "Spend cap per message in USD (0 = none)")
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
