package summary

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/monoes/mono-agent/internal/workflow"
)

// WorkflowSource is the slice of *workflow.HybridWorkflowStore this package reads.
type WorkflowSource interface {
	ListWorkflows(ctx context.Context, profileID string) ([]workflow.Workflow, error)
	GetWorkflow(ctx context.Context, id string) (*workflow.Workflow, error)
}

type WorkflowsSection struct {
	Total  int    `json:"total"`
	Active int    `json:"active"`
	Error  string `json:"error,omitempty"`
}

type ExecCounts struct {
	Total     int `json:"total"`
	Success   int `json:"success"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

// ExecRow is one run; its fields match the desktop app's
// WorkflowExecutionSummary so the binding can unmarshal it unchanged.
type ExecRow struct {
	ID           string `json:"id"`
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	Status       string `json:"status"`
	TriggerType  string `json:"trigger_type"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	CreatedAt    string `json:"created_at"`
	Error        string `json:"error"`
}

type ExecutionsSection struct {
	Running int        `json:"running"`
	Queued  int        `json:"queued"`
	Waiting int        `json:"waiting"`
	Last24h ExecCounts `json:"last_24h"`
	Recent  []ExecRow  `json:"recent"`
	Error   string     `json:"error,omitempty"`
}

type ScheduleRow struct {
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	NodeID       string `json:"node_id"`
	Cron         string `json:"cron"`
	Timezone     string `json:"timezone"`
	NextRun      string `json:"next_run"`
}

type ScheduleIssue struct {
	WorkflowID string `json:"workflow_id"`
	NodeID     string `json:"node_id"`
	Error      string `json:"error"`
}

type SchedulesSection struct {
	DaemonRunning bool            `json:"daemon_running"`
	Upcoming      []ScheduleRow   `json:"upcoming"` // soonest first, one per schedule node
	Invalid       []ScheduleIssue `json:"invalid"`
	Error         string          `json:"error,omitempty"`
}

const recentLimit = 15

func workflowsSection(ctx context.Context, o Options) *WorkflowsSection {
	s := &WorkflowsSection{}
	if o.Workflows == nil {
		s.Error = "workflow store unavailable"
		return s
	}
	wfs, err := o.Workflows.ListWorkflows(ctx, o.ProfileID)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	s.Total = len(wfs)
	for _, w := range wfs {
		if w.IsActive {
			s.Active++
		}
	}
	return s
}

func executionsSection(ctx context.Context, o Options) *ExecutionsSection {
	s := &ExecutionsSection{Recent: []ExecRow{}}
	if o.DB == nil {
		s.Error = noDB
		return s
	}
	err := o.DB.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(status = 'RUNNING'),0), COALESCE(SUM(status = 'QUEUED'),0), COALESCE(SUM(status = 'WAITING'),0)
		FROM workflow_executions WHERE profile_id = ?`, o.ProfileID).Scan(&s.Running, &s.Queued, &s.Waiting)
	if err == nil {
		err = o.DB.QueryRowContext(ctx, `SELECT COUNT(*),
			COALESCE(SUM(status IN ('SUCCESS','SUCCESS_WITH_ERRORS','COMPLETED')),0),
			COALESCE(SUM(status = 'FAILED'),0), COALESCE(SUM(status = 'CANCELLED'),0)
			FROM workflow_executions WHERE profile_id = ? AND `+sinceExpr("created_at")+` >= julianday(?)`,
			o.ProfileID, sqlTime(o.Now.Add(-24*time.Hour))).
			Scan(&s.Last24h.Total, &s.Last24h.Success, &s.Last24h.Failed, &s.Last24h.Cancelled)
	}
	if err == nil {
		s.Recent, err = RecentExecutions(ctx, o.DB, o.ProfileID, recentLimit)
	}
	s.Error = errString(err)
	return s
}

// RecentExecutions is the profile-wide run list, newest first — the
// summary's recent runs and `workflow executions --all`.
func RecentExecutions(ctx context.Context, db *sql.DB, profileID string, limit int) ([]ExecRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT e.id, e.workflow_id, COALESCE(w.name,''), e.status,
		COALESCE(e.trigger_type,''), COALESCE(e.started_at,''), COALESCE(e.finished_at,''),
		COALESCE(e.created_at,''), COALESCE(e.error_message,'')
		FROM workflow_executions e LEFT JOIN workflows w ON w.id = e.workflow_id
		WHERE e.profile_id = ? ORDER BY `+sinceExpr("e.created_at")+` DESC, e.rowid DESC LIMIT ?`, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExecRow{}
	for rows.Next() {
		var r ExecRow
		var started, finished, created any
		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.WorkflowName, &r.Status, &r.TriggerType,
			&started, &finished, &created, &r.Error); err != nil {
			return nil, err
		}
		r.StartedAt, r.FinishedAt, r.CreatedAt = timeString(started), timeString(finished), timeString(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// timeString renders a TIMESTAMP column as RFC3339 UTC whether the driver
// handed back a time.Time or the stored text; unparseable text passes through.
func timeString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case time.Time:
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	case []byte:
		return normaliseTime(string(t))
	case string:
		return normaliseTime(t)
	default:
		return fmt.Sprint(t)
	}
}

func normaliseTime(s string) string {
	if s == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}

// cronParser accepts what the scheduler does (internal/scheduler uses
// cron.WithSeconds: seconds required, descriptors like "@every 5m" allowed).
var cronParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func schedulesSection(ctx context.Context, o Options) *SchedulesSection {
	s := &SchedulesSection{Upcoming: []ScheduleRow{}, Invalid: []ScheduleIssue{}}
	if o.DaemonRunning != nil {
		s.DaemonRunning = o.DaemonRunning()
	}
	if o.Workflows == nil {
		s.Error = "workflow store unavailable"
		return s
	}
	wfs, err := o.Workflows.ListWorkflows(ctx, o.ProfileID)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	for i := range wfs {
		if !wfs[i].IsActive {
			continue
		}
		wf := &wfs[i]
		if len(wf.Nodes) == 0 { // list rows may omit nodes; load the full workflow
			if full, err := o.Workflows.GetWorkflow(ctx, wf.ID); err == nil && full != nil {
				wf = full
			}
		}
		for _, n := range wf.Nodes {
			if n.Type != "trigger.schedule" || n.Disabled {
				continue
			}
			if n.Config == nil {
				_ = n.ParseConfig()
			}
			row, err := nextRun(wf, n, o.Now)
			if err != nil {
				s.Invalid = append(s.Invalid, ScheduleIssue{WorkflowID: wf.ID, NodeID: n.ID, Error: err.Error()})
				continue
			}
			s.Upcoming = append(s.Upcoming, row)
		}
	}
	sort.SliceStable(s.Upcoming, func(i, j int) bool { return s.Upcoming[i].NextRun < s.Upcoming[j].NextRun })
	return s
}

// nextRun builds the spec exactly as trigger_manager.activateSchedule does
// ("CRON_TZ=<tz> <cron>", timezone defaulting to UTC).
func nextRun(wf *workflow.Workflow, n workflow.WorkflowNode, now time.Time) (ScheduleRow, error) {
	spec, _ := n.Config["cron"].(string)
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ScheduleRow{}, errors.New(`missing "cron"`)
	}
	tz, _ := n.Config["timezone"].(string)
	if tz == "" {
		tz = "UTC"
	}
	sched, err := cronParser.Parse(fmt.Sprintf("CRON_TZ=%s %s", tz, spec))
	if err != nil {
		return ScheduleRow{}, err
	}
	next := sched.Next(now)
	if next.IsZero() {
		return ScheduleRow{}, errors.New("schedule never fires")
	}
	return ScheduleRow{WorkflowID: wf.ID, WorkflowName: wf.Name, NodeID: n.ID, Cron: spec, Timezone: tz,
		NextRun: next.UTC().Format(time.RFC3339)}, nil
}
