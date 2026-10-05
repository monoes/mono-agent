package monomind

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func budgetReport(t *testing.T, def, events string) *BudgetReport {
	t.Helper()
	rep, err := SectionBudgets(goldenBytes(t, def), SplitEventLines(goldenBytes(t, events)))
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func scopeByName(t *testing.T, r *BudgetReport, name string) ScopeBudget {
	t.Helper()
	for _, s := range r.Sections {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no section %q in %+v", name, r.Sections)
	return ScopeBudget{}
}

// The recorded 2.24.1 run (real): sections without budgets, usage events
// with cost_usd null. Nothing is allocated, cost is unknown (never $0), and
// the token total is the sum of the recorded usage events.
func TestSectionBudgets_RecordedRunHasNoBudget(t *testing.T) {
	rep := budgetReport(t, "org-sec.json", "events.ndjson")
	if rep.CostComplete {
		t.Error("recorded usage events carry cost_usd null: cost must be incomplete, not $0")
	}
	if rep.Total.State != BudgetUnallocated || rep.Total.AllocationUSD != nil {
		t.Errorf("total = %+v, want unallocated", rep.Total)
	}
	if len(rep.Sections) != 2 || rep.Sections[0].Name != "drafting" || rep.Sections[1].Name != "review" {
		t.Fatalf("sections = %+v, want drafting, review in file order", rep.Sections)
	}
	for _, s := range rep.Sections {
		if s.State != BudgetUnallocated || !s.CostUnknown && s.Name == "drafting" {
			t.Errorf("section %s = %+v", s.Name, s)
		}
	}
	if rep.Reserve == nil || rep.Reserve.Roles[0].ID != "lead" {
		t.Errorf("reserve = %+v, want the root", rep.Reserve)
	}
	// Five recorded usage events: lead 30+30, writer 30, checker 60+15.
	var sum int64
	for _, s := range append(append([]ScopeBudget{}, rep.Sections...), *rep.Reserve) {
		for _, r := range s.Roles {
			sum += r.Tokens
		}
	}
	if rep.TotalTokens != 165 || sum != 165 {
		t.Errorf("total tokens = %d, per-role sum %d, want 165", rep.TotalTokens, sum)
	}
}

// SYNTHETIC (see the README): spend per section, role caps, the warning and
// the soft-closure moment, and totals equal to the sum of every usage event.
func TestSectionBudgets_SyntheticSpendAndSoftClosure(t *testing.T) {
	rep := budgetReport(t, "synthetic-org-budget.json", "synthetic-events-budget.ndjson")
	d := scopeByName(t, rep, "drafting")
	if !near(d.SpentUSD, 1.0) || *d.AllocationUSD != 1 || d.State != BudgetClosed {
		t.Errorf("drafting = %+v, want $1.00 of $1, closed", d)
	}
	if !d.SoftClosed || d.ClosedAt != 1791230005001 || d.WarnedAt != 1791230003001 {
		t.Errorf("drafting closure = closed:%v at %d, warned %d", d.SoftClosed, d.ClosedAt, d.WarnedAt)
	}
	if len(d.ClosedRoles) != 2 || len(d.HeldTasks) != 1 || d.HeldTasks[0] != "t2" {
		t.Errorf("drafting closed roles %v held %v", d.ClosedRoles, d.HeldTasks)
	}
	if !near(*d.RemainingUSD, 0) || !near(d.RoleCapSumUSD, 1.0) {
		t.Errorf("drafting remaining %v cap sum %v", *d.RemainingUSD, d.RoleCapSumUSD)
	}
	var writer RoleBudget
	for _, r := range d.Roles {
		if r.ID == "writer" {
			writer = r
		}
	}
	if !near(writer.SpentUSD, 0.6) || *writer.CapUSD != 0.6 || writer.State != BudgetClosed {
		t.Errorf("writer = %+v, want $0.60 of its $0.60 cap", writer)
	}
	r := scopeByName(t, rep, "review")
	if !near(r.SpentUSD, 0.42) || r.State != BudgetWarn || r.SoftClosed || r.WarnedAt == 0 {
		t.Errorf("review = %+v, want $0.42 of $0.50, warn, not closed", r)
	}
	if rep.Reserve == nil || !near(rep.Reserve.SpentUSD, 0.05) || !near(*rep.Reserve.AllocationUSD, 0.5) {
		t.Errorf("reserve = %+v, want $0.05 of $0.50 (2.00 - 1.00 - 0.50)", rep.Reserve)
	}
	// Totals: the sum of every usage event's cost_usd (what `org report`
	// prints as "Cost"), split with nothing lost or counted twice.
	if want := 0.05 + 0.42 + 0.40 + 0.10 + 0.18 + 0.32; !near(rep.Total.SpentUSD, want) {
		t.Errorf("total = %v, want %v", rep.Total.SpentUSD, want)
	}
	if sum := d.SpentUSD + r.SpentUSD + rep.Reserve.SpentUSD + rep.UnattributedUSD; !near(sum, rep.Total.SpentUSD) {
		t.Errorf("sections+reserve = %v, total %v", sum, rep.Total.SpentUSD)
	}
	if !rep.CostComplete || rep.Total.State != BudgetOK || rep.TotalTokens != 6000 {
		t.Errorf("total = %+v complete=%v tokens=%d", rep.Total, rep.CostComplete, rep.TotalTokens)
	}
}

func TestSectionBudgets_ReopenedClearsSoftClosure(t *testing.T) {
	def := goldenBytes(t, "synthetic-org-budget.json")
	events := append(SplitEventLines(goldenBytes(t, "synthetic-events-budget.ndjson")),
		[]byte(`{"ts":1791230009000,"org":"bud","run":"r","type":"audit","reason":"section-budget-reopened","data":{"scope":"section:drafting","spentUsd":1,"allocationUsd":2}}`))
	rep, err := SectionBudgets(def, events)
	if err != nil {
		t.Fatal(err)
	}
	if d := scopeByName(t, rep, "drafting"); d.SoftClosed || d.ReopenedAt != 1791230009000 {
		t.Errorf("drafting = %+v, want reopened", d)
	}
}

func TestSectionBudgets_ToleratesJunk(t *testing.T) {
	rep, err := SectionBudgets([]byte(`{"roles":[{"id":"a","reports_to":null}]}`),
		[][]byte{[]byte("not json"), []byte(`{"type":"usage","from":"gone","data":{"cost_usd":0.5,"tokens":3}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !near(rep.UnattributedUSD, 0.5) || !near(rep.Total.SpentUSD, 0.5) {
		t.Errorf("a removed role's spend counts in the org total only: %+v", rep)
	}
	if _, err := SectionBudgets([]byte("nope"), nil); err == nil {
		t.Error("an unreadable definition is an error")
	}
}

// OrgBudget end to end through the replaying fake monomind: the definition
// from the project, the events from `org events`. SYNTHETIC input.
func TestOrgBudget_ThroughOrgEvents(t *testing.T) {
	goldenMonomind(t, "list-all.json", "status-stopped.json", "synthetic-events-budget.ndjson")
	root := t.TempDir()
	dir := filepath.Join(root, ".monomind", "orgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bud.json"), goldenBytes(t, "synthetic-org-budget.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := OrgBudget(context.Background(), root, "bud", "")
	if err != nil {
		t.Fatal(err)
	}
	if d := scopeByName(t, rep, "drafting"); !d.SoftClosed || !near(d.SpentUSD, 1.0) {
		t.Errorf("drafting = %+v", d)
	}
	for _, bad := range []string{"", "../x", ".hidden", "a/b"} {
		if _, err := OrgBudget(context.Background(), root, bad, ""); err == nil {
			t.Errorf("org name %q must be refused", bad)
		}
	}
}
