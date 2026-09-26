//go:build !windows

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The dashboard bindings shell out to the CLI (summary, org summary,
// workflow executions --all) and never read the database themselves.
func TestSummaryBindingsShellOut(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	bin := fakeCLI(t, `echo "$*" >> '`+log+`'
case "$*" in
  *"summary --section"*) echo '{"v":1,"workflows":{"total":3,"active":2},"people":{"total":9,"lists":1},"accounts":{"active":1,"sessions":[{"platform":"linkedin","username":"me","expiry":"2026-10-01T00:00:00Z","status":"active"},{"platform":"x","username":"me","expiry":"2026-09-01T00:00:00Z","status":"expired"}]},"executions":{"running":1,"queued":2}}' ;;
  *"org summary --fast"*) echo '{"v":1,"fast":true,"orgs":[]}' ;;
  *"org summary"*) echo '{"v":1,"fast":false,"orgs":[]}' ;;
  *" summary"*) echo '{"v":1}' ;;
  *"executions --all"*) echo '[{"id":"e1","workflow_id":"w1","workflow_name":"A","status":"SUCCESS"}]' ;;
esac
`)
	t.Setenv("MONOAGENTCLI_BIN", bin)
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")

	if got := strings.TrimSpace(a.GetSummary()); got != `{"v":1}` {
		t.Fatalf("GetSummary = %q", got)
	}
	if got := a.GetOrgSummary(true); !strings.Contains(got, `"fast":true`) {
		t.Fatalf("GetOrgSummary(true) = %q", got)
	}
	if got := a.GetOrgSummary(false); !strings.Contains(got, `"fast":false`) {
		t.Fatalf("GetOrgSummary(false) = %q", got)
	}
	st := a.GetDashboardStats()
	if st.TotalWorkflows != 3 || st.TotalPeople != 9 || st.TotalLists != 1 || st.ActiveSessions != 1 ||
		st.ExecutionsByStatus["RUNNING"] != 1 || st.ExecutionsByStatus["QUEUED"] != 2 {
		t.Fatalf("GetDashboardStats = %+v", st)
	}
	if len(st.Sessions) != 2 || !st.Sessions[0].Active || st.Sessions[1].Active {
		t.Fatalf("sessions = %+v", st.Sessions)
	}
	rows, err := a.GetRecentExecutions(30)
	if err != nil || len(rows) != 1 || rows[0].WorkflowName != "A" {
		t.Fatalf("GetRecentExecutions = %+v, %v", rows, err)
	}
	want := []string{
		"--profile work --json summary",
		"--profile work --json org summary --fast",
		"--profile work --json org summary",
		"--profile work --json summary --section workflows,executions,people,accounts",
		"--profile work --json workflow executions --all --limit 30",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A CLI failure leaves the Sidebar/StatusBar with zeroed stats, not a crash.
func TestDashboardStatsSurvivesCLIFailure(t *testing.T) {
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, "echo boom >&2; exit 1\n"))
	a := newTestApp(t)
	a.ctx = context.Background()
	st := a.GetDashboardStats()
	if st.TotalWorkflows != 0 || st.ExecutionsByStatus == nil {
		t.Fatalf("stats = %+v", st)
	}
	if _, err := a.GetRecentExecutions(5); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("GetRecentExecutions err = %v", err)
	}
}

// The HIL badge's poll: `summary --section hil`, never `hil list --suggest`.
func TestGetSummarySectionsArgv(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
echo '{"v":1,"hil":{"total":1}}'
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")
	if got := strings.TrimSpace(a.GetSummarySections("hil")); got != `{"v":1,"hil":{"total":1}}` {
		t.Fatalf("GetSummarySections = %q", got)
	}
	if got := strings.Join(loggedArgs(t, log), "\n"); got != "--profile work --json summary --section hil" {
		t.Fatalf("argv = %q", got)
	}
}
