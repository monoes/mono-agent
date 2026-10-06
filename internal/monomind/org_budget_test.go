package monomind

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
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

// REAL run (budget-*.ndjson/json: a sections org with section and role USD
// budgets, run by monomind 2.24.1 with a scripted grok stand-in that reports a
// real total_cost_usd). Each section's spend, the warnings and the soft-closure
// moment come out of the recorded bus, and the totals equal monomind's own.
func TestSectionBudgets_RecordedBudgetedRun(t *testing.T) {
	rep := budgetReport(t, "budget-org.json", "budget-events.ndjson")
	d := scopeByName(t, rep, "drafting")
	if !near(d.SpentUSD, 0.0725) || *d.AllocationUSD != 0.05 || d.State != BudgetClosed {
		t.Errorf("drafting = %+v, want $0.0725 of $0.05, closed", d)
	}
	if !d.SoftClosed || d.ClosedAt == 0 || d.WarnedAt == 0 || d.WarnedAt >= d.ClosedAt {
		t.Errorf("drafting: soft-closed %v at %d, warned at %d (the warning comes first)", d.SoftClosed, d.ClosedAt, d.WarnedAt)
	}
	if len(d.ClosedRoles) != 1 || d.ClosedRoles[0] != "writer" || len(d.HeldTasks) != 0 {
		t.Errorf("drafting closed %v held %v, want writer closed, nothing held", d.ClosedRoles, d.HeldTasks)
	}
	r := scopeByName(t, rep, "review")
	if !near(r.SpentUSD, 0.051) || !r.SoftClosed || r.State != BudgetClosed {
		t.Errorf("review = %+v, want $0.051 of $0.04, soft-closed", r)
	}
	if rep.Reserve == nil || !near(rep.Reserve.SpentUSD, 0.016) || !near(*rep.Reserve.AllocationUSD, 0.06) || rep.Reserve.State != BudgetOK {
		t.Errorf("reserve = %+v, want $0.016 of $0.06 (0.15 - 0.05 - 0.04), ok", rep.Reserve)
	}
	if !near(rep.Total.SpentUSD, 0.1395) || rep.Total.State != BudgetWarn || rep.Total.WarnedAt == 0 || rep.Total.SoftClosed {
		t.Errorf("total = %+v, want $0.1395 of $0.15, warn, not closed", rep.Total)
	}
	w := d.Roles[0]
	if w.ID != "writer" || !near(w.SpentUSD, 0.0725) || *w.CapUSD != 0.05 || w.State != BudgetClosed {
		t.Errorf("writer = %+v", w)
	}
	if !rep.CostComplete || rep.TotalTokens != 22500 {
		t.Errorf("complete=%v tokens=%d, want true, 22500", rep.CostComplete, rep.TotalTokens)
	}
	if sum := d.SpentUSD + r.SpentUSD + rep.Reserve.SpentUSD + rep.UnattributedUSD; !near(sum, rep.Total.SpentUSD) {
		t.Errorf("sections+reserve = %v, total %v", sum, rep.Total.SpentUSD)
	}
}

var reportRowRe = regexp.MustCompile(`(section \w+|root reserve|org): spent \$([0-9.]+) of \$([0-9.]+)`)

// Acceptance: the numbers equal what monomind itself printed for the same run
// (`org report`'s section table, `org costs`, `org report --format json`).
func TestSectionBudgets_MatchesMonomindsOwnReport(t *testing.T) {
	rep := budgetReport(t, "budget-org.json", "budget-events.ndjson")
	got := map[string]*ScopeBudget{"section drafting": &rep.Sections[0], "section review": &rep.Sections[1], "root reserve": rep.Reserve, "org": &rep.Total}
	rows := reportRowRe.FindAllStringSubmatch(string(goldenBytes(t, "budget-report.txt")), -1)
	if len(rows) != 4 {
		t.Fatalf("monomind's report has %d budget rows, want 4", len(rows))
	}
	for _, m := range rows {
		s := got[m[1]]
		if spent := fmt.Sprintf("%.4f", s.SpentUSD); spent != m[2] {
			t.Errorf("%s: spent %s, monomind's report says %s", m[1], spent, m[2])
		}
		if alloc := fmt.Sprintf("%.2f", *s.AllocationUSD); alloc != m[3] {
			t.Errorf("%s: allocation %s, monomind's report says %s", m[1], alloc, m[3])
		}
	}
	var costs struct {
		Totals struct {
			Tokens  int64   `json:"tokens"`
			CostUSD float64 `json:"cost_usd"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(goldenBytes(t, "budget-costs.json"), &costs); err != nil {
		t.Fatal(err)
	}
	if !near(rep.Total.SpentUSD, costs.Totals.CostUSD) || rep.TotalTokens != costs.Totals.Tokens {
		t.Errorf("total %v / %d tokens, org costs says %v / %d", rep.Total.SpentUSD, rep.TotalTokens, costs.Totals.CostUSD, costs.Totals.Tokens)
	}
}

func TestSectionBudgets_ReopenedClearsSoftClosure(t *testing.T) {
	def := goldenBytes(t, "budget-org.json")
	events := append(SplitEventLines(goldenBytes(t, "budget-events.ndjson")),
		[]byte(`{"ts":1791230009000,"org":"bud","run":"r","type":"audit","reason":"section-budget-reopened","data":{"scope":"section:drafting","spentUsd":0.0725,"allocationUsd":0.2}}`))
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
// from the project, the events from `org events` (recorded run).
func TestOrgBudget_ThroughOrgEvents(t *testing.T) {
	goldenMonomind(t, "list-all.json", "budget-status.json", "budget-events.ndjson")
	root := t.TempDir()
	dir := filepath.Join(root, ".monomind", "orgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bud.json"), goldenBytes(t, "budget-org.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := OrgBudget(context.Background(), root, "bud", "")
	if err != nil {
		t.Fatal(err)
	}
	if d := scopeByName(t, rep, "drafting"); !d.SoftClosed || !near(d.SpentUSD, 0.0725) {
		t.Errorf("drafting = %+v", d)
	}
	for _, bad := range []string{"", "../x", ".hidden", "a/b"} {
		if _, err := OrgBudget(context.Background(), root, bad, ""); err == nil {
			t.Errorf("org name %q must be refused", bad)
		}
	}
}
