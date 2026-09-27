package summary

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func part(id, name string, s Summary) ProfilePart { return ProfilePart{ID: id, Name: name, S: s} }

func TestMergeSumsCountsAndTagsRows(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	at := func(m int) string { return now.Add(time.Duration(m) * time.Minute).Format(time.RFC3339) }
	work := Summary{
		Workflows: &WorkflowsSection{Total: 3, Active: 2},
		Executions: &ExecutionsSection{Running: 1, Last24h: ExecCounts{Total: 5, Failed: 1},
			Recent: []ExecRow{{ID: "w1", CreatedAt: at(-1)}, {ID: "w2", CreatedAt: at(-30)}}},
		Schedules:    &SchedulesSection{DaemonRunning: true, Upcoming: []ScheduleRow{{WorkflowID: "wa", NextRun: at(50)}}},
		HIL:          &HILSection{Total: 2, WorkflowPending: 2, OldestWaitingSince: at(-90)},
		Accounts:     &AccountsSection{Sessions: []SessionRow{{Platform: "linkedin", Username: "work"}}, Active: 1},
		Applications: &ApplicationsSection{ByStatus: map[string]int{"pending": 2}},
	}
	home := Summary{
		Workflows: &WorkflowsSection{Total: 1, Active: 1, Error: "boom"},
		Executions: &ExecutionsSection{Waiting: 2, Last24h: ExecCounts{Total: 1},
			Recent: []ExecRow{{ID: "h1", CreatedAt: at(-10)}}},
		Schedules:    &SchedulesSection{Upcoming: []ScheduleRow{{WorkflowID: "hb", NextRun: at(5)}}},
		HIL:          &HILSection{Total: 1, OldestWaitingSince: at(-200)},
		Accounts:     &AccountsSection{Sessions: []SessionRow{{Platform: "linkedin", Username: "me"}}, Expired: 1},
		Applications: &ApplicationsSection{ByStatus: map[string]int{"pending": 1, "sent": 4}},
	}
	shared := Summary{GeneratedAt: "g", Services: &ServicesSection{}}
	got := Merge([]ProfilePart{part("p-work", "Work", work), part("p-home", "Personal", home)}, shared, "p-home")

	if got.Scope != ScopeGlobal || got.Services == nil || got.GeneratedAt != "g" || got.ProfileID != "" {
		t.Fatalf("envelope: scope=%q services=%v generated=%q profile=%q", got.Scope, got.Services, got.GeneratedAt, got.ProfileID)
	}
	if w := got.Workflows; w.Total != 4 || w.Active != 3 || w.Error != "Personal: boom" {
		t.Errorf("workflows = %+v", w)
	}
	ex := got.Executions
	if ex.Running != 1 || ex.Waiting != 2 || ex.Last24h.Total != 6 || ex.Last24h.Failed != 1 {
		t.Errorf("executions = %+v", ex)
	}
	for i, id := range []string{"w1", "h1", "w2"} {
		if ex.Recent[i].ID != id {
			t.Fatalf("recent order = %+v, want w1 h1 w2", ex.Recent)
		}
	}
	if ex.Recent[1].ProfileID != "p-home" || ex.Recent[1].ProfileName != "Personal" {
		t.Errorf("row not tagged: %+v", ex.Recent[1])
	}
	if s := got.Schedules; !s.DaemonRunning || s.Upcoming[0].WorkflowID != "hb" || s.Upcoming[0].ProfileName != "Personal" {
		t.Errorf("schedules = %+v", s)
	}
	if h := got.HIL; h.Total != 3 || h.WorkflowPending != 2 || h.OldestWaitingSince != at(-200) {
		t.Errorf("hil = %+v", h)
	}
	if a := got.Accounts; len(a.Sessions) != 2 || a.Sessions[0].ProfileID != "p-work" || a.Active != 1 || a.Expired != 1 {
		t.Errorf("accounts = %+v", a)
	}
	if a := got.Applications; a.ByStatus["pending"] != 3 || a.ByStatus["sent"] != 4 {
		t.Errorf("applications = %+v", a)
	}
	if p := got.Profiles; len(p) != 2 || p[0].Current || !p[1].Current || p[0].Failed24h != 1 || p[0].Running != 1 ||
		p[1].WaitingForYou != 1 || p[1].Error == "" {
		t.Errorf("profiles = %+v", p)
	}
	if got.People != nil || got.Vault != nil {
		t.Error("a section no profile reported must stay nil")
	}
}

func TestSortRecentTrims(t *testing.T) {
	rows := make([]ExecRow, 20)
	for i := range rows {
		rows[i] = ExecRow{ID: fmt.Sprint(i), CreatedAt: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC).Format(time.RFC3339)}
	}
	got := SortRecent(rows, 15)
	if len(got) != 15 || got[0].ID != "19" || got[14].ID != "5" {
		t.Fatalf("got %d rows, first %s, last %s", len(got), got[0].ID, got[len(got)-1].ID)
	}
	if all := SortRecent(rows, -1); len(all) != 20 {
		t.Fatalf("limit -1 keeps all, got %d", len(all))
	}
}

func TestBuildSaysProfileScope(t *testing.T) {
	if s := Build(context.Background(), Options{Sections: map[string]bool{}}); s.Scope != ScopeProfile {
		t.Fatalf("scope = %q", s.Scope)
	}
}
