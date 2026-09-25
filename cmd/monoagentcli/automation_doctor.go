package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/automation"
	tw "github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
)

// doctorSelectorJSON is one selector of `automation doctor --json`.
type doctorSelectorJSON struct {
	Key      string `json:"key"`
	OK       int    `json:"ok"`
	Fail     int    `json:"fail"`
	Healed   int    `json:"healed"`
	LastOK   string `json:"lastOk"`   // RFC3339, "" when never
	LastFail string `json:"lastFail"` // RFC3339, "" when never
	Status   string `json:"status"`   // ok | decaying | broken
}

// doctorAutomationJSON is one automation of `automation doctor --json`
// (contracts §5), plus a few fields the human output needs.
type doctorAutomationJSON struct {
	ID                string                 `json:"id"`
	Version           string                 `json:"version"`
	Enabled           bool                   `json:"enabled"`
	Available         bool                   `json:"available"`
	UnavailableReason string                 `json:"unavailableReason,omitempty"`
	Native            string                 `json:"native,omitempty"` // requires.native bot id
	Issues            []automation.IssueJSON `json:"issues"`
	Selectors         []doctorSelectorJSON   `json:"selectors"`
	Session           automationSession      `json:"session"`
}

// newAutomationDoctorCmd builds `automation doctor [id]`: registry issues,
// availability (policy, missing native bot, engine), login state and
// selector health for each installed automation.
func newAutomationDoctorCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor [id]",
		Short: "Check installed automations: issues, login state and selector health",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openAutomationRegistry()
			if err != nil {
				return err
			}
			var infos []automation.InstalledInfo
			if len(args) == 1 {
				info, err := reg.Info(args[0])
				if err != nil {
					return err
				}
				infos = []automation.InstalledInfo{*info}
			} else if infos, err = reg.List(false); err != nil {
				return err
			}
			sessions, health := loadDoctorState(cfg)
			rows := make([]doctorAutomationJSON, 0, len(infos))
			for _, info := range infos {
				rows = append(rows, doctorAutomation(reg, info, sessions.lookup(info.ID), health[info.ID]))
			}
			out := cmd.OutOrStdout()
			if cfg.JSONOutput {
				return writeJSONTo(out, map[string]any{"automations": rows})
			}
			printAutomationDoctor(out, rows)
			return nil
		},
	}
}

// loadDoctorState reads sessions and selector health. Like `automation
// list`, it never creates a database: no DB yet means no logins and no runs.
func loadDoctorState(cfg *globalConfig) (sessionIndex, map[string][]automation.SelectorHealth) {
	health := map[string][]automation.SelectorHealth{}
	if _, err := os.Stat(expandPath(cfg.DBPath)); err != nil {
		return sessionIndex{}, health
	}
	db, err := initDB(cfg)
	if err != nil {
		stderrf("warning: login state and selector health unavailable: %v\n", err)
		return sessionIndex{}, health
	}
	defer db.Close()
	return querySessionIndex(db.DB, cfg.ProfileID, time.Now()), groupSelectorHealth(db.DB)
}

func groupSelectorHealth(db *sql.DB) map[string][]automation.SelectorHealth {
	out := map[string][]automation.SelectorHealth{}
	rows, err := automation.LoadSelectorHealth(db, "")
	if err != nil {
		stderrf("warning: selector health unavailable: %v\n", err)
		return out
	}
	for _, h := range rows {
		out[h.AutomationID] = append(out[h.AutomationID], h)
	}
	return out
}

// doctorAutomation assembles one automation's report. Selectors are the
// package's declared keys plus any key with recorded health, sorted.
func doctorAutomation(reg *automation.Registry, info automation.InstalledInfo,
	session automationSession, health []automation.SelectorHealth) doctorAutomationJSON {
	row := doctorAutomationJSON{
		ID: info.ID, Version: info.Version, Enabled: info.Enabled,
		Available: info.Available, UnavailableReason: info.UnavailableReason,
		Issues: []automation.IssueJSON{}, Selectors: []doctorSelectorJSON{}, Session: session,
	}
	byKey := map[string]doctorSelectorJSON{}
	pkg, err := reg.Get(info.ID)
	if err != nil {
		row.Issues = append(row.Issues, automation.IssueJSON{Severity: "error", Code: "open_failed", Message: err.Error()})
	} else {
		row.Native = pkg.Manifest.Requires.Native
		row.Issues = append(row.Issues, automation.Validate(pkg)...)
		if sels, err := pkg.Selectors(); err == nil {
			for key := range sels {
				byKey[key] = doctorSelectorJSON{Key: key, Status: automation.HealthOK}
			}
		}
	}
	if !info.Available {
		row.Issues = append(row.Issues, automation.IssueJSON{Severity: "error", Code: "unavailable", Message: info.UnavailableReason})
	}
	if info.PendingUpdate != "" {
		row.Issues = append(row.Issues, automation.IssueJSON{Severity: "warning", Code: "pending_update",
			Message: fmt.Sprintf("built-in %s is held back because this copy was modified", info.PendingUpdate)})
	}
	for _, h := range health {
		byKey[h.Key] = doctorSelectorJSON{Key: h.Key, OK: h.OK, Fail: h.Fail, Healed: h.Healed,
			LastOK: rfc3339OrEmpty(h.LastOK), LastFail: rfc3339OrEmpty(h.LastFail), Status: h.Status}
	}
	for _, s := range byKey {
		row.Selectors = append(row.Selectors, s)
	}
	sort.Slice(row.Selectors, func(i, j int) bool { return row.Selectors[i].Key < row.Selectors[j].Key })
	return row
}

func rfc3339OrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// printAutomationDoctor prints one table row per automation, then the
// issues and the selectors that need attention.
func printAutomationDoctor(out io.Writer, rows []doctorAutomationJSON) {
	if len(rows) == 0 {
		fmt.Fprintln(out, "No automations installed.")
		return
	}
	table := newPlainTable(out, []string{"ID", "Version", "Status", "Login", "Issues", "Selectors ok/decaying/broken"},
		[]tw.Align{tw.AlignLeft, tw.AlignLeft, tw.AlignLeft, tw.AlignLeft, tw.AlignRight, tw.AlignLeft})
	for _, r := range rows {
		status := "ok"
		switch {
		case !r.Available:
			status = "unavailable"
		case !r.Enabled:
			status = "disabled"
		}
		login := "-"
		if r.Session.LoggedIn {
			login = r.Session.Username
		} else if r.Session.Username != "" {
			login = r.Session.Username + " (expired)"
		}
		counts := map[string]int{}
		for _, s := range r.Selectors {
			counts[s.Status]++
		}
		table.Append([]string{r.ID, r.Version, status, login, fmt.Sprint(len(r.Issues)),
			fmt.Sprintf("%d/%d/%d", counts[automation.HealthOK], counts[automation.HealthDecaying], counts[automation.HealthBroken])})
	}
	table.Render()
	for _, r := range rows {
		var lines []string
		for _, is := range r.Issues {
			lines = append(lines, fmt.Sprintf("  %-7s %s: %s", is.Severity, is.Code, is.Message))
		}
		for _, s := range r.Selectors {
			if s.Status == automation.HealthOK {
				continue
			}
			lines = append(lines, fmt.Sprintf("  %-7s selector %s: ok %d, healed %d, failed %d (last fail %s)",
				s.Status, s.Key, s.OK, s.Healed, s.Fail, orDash(s.LastFail)))
		}
		if len(lines) > 0 {
			fmt.Fprintf(out, "\n%s\n%s\n", r.ID, strings.Join(lines, "\n"))
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
