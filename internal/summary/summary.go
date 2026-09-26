// Package summary builds the dashboard's at-a-glance roll-up
// (`monoagentcli summary`). Every section is read-only and local: SQLite,
// files under the profile, and at most a loopback probe of the extension
// bridge. Nothing here may call Jev, monomind, or the network — the GUI
// polls this every 15 seconds.
package summary

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/recording"
)

// SectionNames lists every section in output order; `--section` accepts these.
var SectionNames = []string{"workflows", "executions", "schedules", "hil", "people", "activity",
	"applications", "services", "automations", "recordings", "jev", "accounts", "vault"}

// Options carries the stores the sections read. A nil source makes its
// section report an error instead of failing the whole summary.
type Options struct {
	DB            *sql.DB
	ProfileID     string
	Now           time.Time
	Sections      map[string]bool // nil = all
	Workflows     WorkflowSource
	DaemonRunning func() bool
	// DaemonSchedules is the live daemon's own next fire time per schedule
	// node, keyed "<workflow_id>/<node_id>" (nil when no daemon runs).
	DaemonSchedules func() map[string]time.Time
	Daemon          func() *DaemonStatus
	Bridge          func() (*BridgeStatus, error)
	OrgServe        func() (running bool, orgs []string)
	Automations     AutomationSource
	Recordings      func() ([]recording.Summary, error)
	Captures        func() ([]capture.Entry, error)
	// SummaryState reports a capture directory's AI-summary state
	// (capturesummary.StateOf); nil leaves the summary counts at zero.
	SummaryState func(captureDir string) string
}

// Summary is the `summary --json` envelope. Unselected sections are nil and omitted.
type Summary struct {
	V            int                  `json:"v"`
	GeneratedAt  string               `json:"generated_at"`
	ProfileID    string               `json:"profile_id"`
	Workflows    *WorkflowsSection    `json:"workflows,omitempty"`
	Executions   *ExecutionsSection   `json:"executions,omitempty"`
	Schedules    *SchedulesSection    `json:"schedules,omitempty"`
	HIL          *HILSection          `json:"hil,omitempty"`
	People       *PeopleSection       `json:"people,omitempty"`
	Activity     *ActivitySection     `json:"activity,omitempty"`
	Applications *ApplicationsSection `json:"applications,omitempty"`
	Services     *ServicesSection     `json:"services,omitempty"`
	Automations  *AutomationsSection  `json:"automations,omitempty"`
	Recordings   *RecordingsSection   `json:"recordings,omitempty"`
	Jev          *JevSection          `json:"jev,omitempty"`
	Accounts     *AccountsSection     `json:"accounts,omitempty"`
	Vault        *VaultSection        `json:"vault,omitempty"`
}

func (o Options) want(name string) bool { return o.Sections == nil || o.Sections[name] }

// Build runs every selected section. A section's failure lands in its own
// Error field; Build itself never fails.
func Build(ctx context.Context, o Options) Summary {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	s := Summary{V: 1, GeneratedAt: o.Now.UTC().Format(time.RFC3339), ProfileID: o.ProfileID}
	if o.want("workflows") {
		s.Workflows = workflowsSection(ctx, o)
	}
	if o.want("executions") {
		s.Executions = executionsSection(ctx, o)
	}
	if o.want("schedules") {
		s.Schedules = schedulesSection(ctx, o)
	}
	if o.want("hil") {
		s.HIL = hilSection(ctx, o)
	}
	if o.want("people") {
		s.People = peopleSection(ctx, o)
	}
	if o.want("activity") {
		s.Activity = activitySection(ctx, o)
	}
	if o.want("applications") {
		s.Applications = applicationsSection(ctx, o)
	}
	if o.want("services") {
		s.Services = servicesSection(o)
	}
	if o.want("automations") {
		s.Automations = automationsSection(o)
	}
	if o.want("recordings") {
		s.Recordings = recordingsSection(o)
	}
	if o.want("jev") {
		s.Jev = jevSection(ctx, o)
	}
	if o.want("accounts") {
		s.Accounts = accountsSection(ctx, o)
	}
	if o.want("vault") {
		s.Vault = vaultSection(ctx, o)
	}
	return s
}

// sinceExpr normalises the three timestamp shapes this DB holds
// ("2006-01-02 15:04:05", RFC3339, and time.Time.String()) to a julianday,
// so "since" filters work on all of them. The offset of time.Time.String()
// values is ignored; every writer of the filtered columns stores UTC.
func sinceExpr(col string) string {
	return fmt.Sprintf("julianday(replace(substr(%s,1,19),'T',' '))", col)
}

// sqlTime is the bound sinceExpr compares against.
func sqlTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// noDB is the error every DB-backed section reports without a database.
const noDB = "database unavailable"
