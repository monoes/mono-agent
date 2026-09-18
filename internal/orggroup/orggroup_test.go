package orggroup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/storage"
)

func share(v float64) *float64 { return &v }

func writeGroup(t *testing.T, root string) {
	t.Helper()
	lead := func(id string) orgdesign.Role {
		return orgdesign.Role{ID: id, Title: id, Type: "boss", Responsibilities: []string{"Lead."}}
	}
	hq := &orgdesign.Doc{Name: "hq", Kind: orgdesign.OrgKindHolding, Status: "stopped", Schedule: json.RawMessage("null"),
		RunConfig: map[string]json.RawMessage{GroupBudgetKey: json.RawMessage("10")},
		Roles:     []orgdesign.Role{lead("ceo")},
		ChildOrgs: []orgdesign.ChildOrg{{Org: "sales", Start: "with_parent", BudgetShare: share(0.5)}, {Org: "growth"}}}
	sales := &orgdesign.Doc{Name: "sales", Status: "stopped", Schedule: json.RawMessage("null"), Roles: []orgdesign.Role{lead("lead")}}
	growth := &orgdesign.Doc{Name: "growth", Status: "stopped", Schedule: json.RawMessage("null"), Roles: []orgdesign.Role{lead("boss")}}
	for _, d := range []*orgdesign.Doc{hq, sales, growth} {
		if _, err := orgdesign.Save(root, d); err != nil {
			t.Fatal(err)
		}
	}
}

func costsEnv(root string, costs map[string]string) *Env {
	return &Env{Root: root, ProfileID: "p",
		Costs: func(_ context.Context, _, org string) (json.RawMessage, error) {
			return json.RawMessage(costs[org]), nil
		},
		StatusFn: func(_ context.Context, _, org string) (json.RawMessage, error) {
			return json.RawMessage(`{"status":"running","run":"run-` + org + `"}`), nil
		},
	}
}

// C-48: cross-org senders listed in a cost table are not counted.
func TestRollupCountsConfiguredRolesOnly(t *testing.T) {
	root := t.TempDir()
	writeGroup(t, root)
	env := costsEnv(root, map[string]string{
		"hq":     `{"items":[{"role":"ceo","cost_usd":1},{"role":"sales:lead","cost_usd":0}]}`,
		"sales":  `{"items":[{"role":"lead","cost_usd":2},{"role":"hq:ceo","cost_usd":7}]}`,
		"growth": `{"items":[{"role":"boss","cost_usd":0.5}]}`,
	})
	st, err := env.GroupStatus(context.Background(), "hq")
	if err != nil {
		t.Fatal(err)
	}
	if st.RollupUS != 3.5 || len(st.Children) != 2 || st.Children[0].CostUSD != 2 || st.Children[1].Start != "on_demand" {
		t.Fatalf("status = %+v", st)
	}
}

func TestCheckStartBudget(t *testing.T) {
	root := t.TempDir()
	writeGroup(t, root)
	ctx := context.Background()
	under := costsEnv(root, map[string]string{"sales": `{"items":[{"role":"lead","cost_usd":1}]}`})
	if err := under.CheckStart(ctx, "hq", "sales"); err != nil {
		t.Fatalf("under budget refused: %v", err)
	}
	overShare := costsEnv(root, map[string]string{"sales": `{"items":[{"role":"lead","cost_usd":5}]}`})
	if err := overShare.CheckStart(ctx, "hq", "sales"); err == nil || !strings.Contains(err.Error(), "share") {
		t.Fatalf("over share: %v", err)
	}
	if err := overShare.CheckStart(ctx, "hq", "growth"); err != nil {
		t.Fatalf("growth has no share and the group is under budget: %v", err)
	}
	overGroup := costsEnv(root, map[string]string{"hq": `{"items":[{"role":"ceo","cost_usd":11}]}`})
	if err := overGroup.CheckStart(ctx, "hq", "growth"); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("over group budget: %v", err)
	}
	if err := under.CheckStart(ctx, "hq", "stranger"); err == nil {
		t.Fatal("started an org that is not a child")
	}
	if err := under.CheckStart(ctx, "sales", "growth"); err == nil {
		t.Fatal("treated a standard org as holding")
	}
}

func TestApplyReportUp(t *testing.T) {
	root := t.TempDir()
	writeGroup(t, root)
	docs, _, _ := orgdesign.LoadAll(root)
	changed := ApplyReportUp(docs)
	if len(changed) != 2 {
		t.Fatalf("changed %d docs, want the two children", len(changed))
	}
	for _, d := range docs {
		boss, _ := d.RootRole()
		lines := strings.Join(boss.Responsibilities, "\n")
		if d.Name == "hq" && strings.Contains(lines, reportUpTag) {
			t.Fatal("holding org got a report-up line")
		}
		if d.Name != "hq" && !strings.Contains(lines, "org_send a short summary") || strings.Count(lines, reportUpTag) > 1 {
			t.Fatalf("%s responsibilities = %q", d.Name, lines)
		}
	}
	if again := ApplyReportUp(docs); len(again) != 0 {
		t.Fatal("report-up not idempotent")
	}
	for _, d := range docs {
		if d.Name == "hq" {
			d.ChildOrgs = d.ChildOrgs[:1]
		}
	}
	changed = ApplyReportUp(docs)
	if len(changed) != 1 || changed[0].Name != "growth" {
		t.Fatalf("removal changed %v", changed)
	}
	boss, _ := changed[0].RootRole()
	if strings.Contains(strings.Join(boss.Responsibilities, ""), reportUpTag) || len(boss.Responsibilities) != 1 {
		t.Fatalf("report-up line not removed: %v", boss.Responsibilities)
	}
}

func TestReportUpWatcherFallback(t *testing.T) {
	root := t.TempDir()
	writeGroup(t, root)
	var sent []orgbridge.SendRequest
	w := &ReportUpWatcher{
		Mux:   orgbridge.NewMux(func(ctx context.Context, _, _, _ string, _ func([]byte)) error { <-ctx.Done(); return nil }),
		Roots: func(context.Context) ([]ProfileRoot, error) { return []ProfileRoot{{ProfileID: "p", Root: root}}, nil },
		Report: func(context.Context, string, string) (json.RawMessage, error) {
			return json.RawMessage(`{"outcome":"done"}`), nil
		},
		Send: func(_ context.Context, r orgbridge.SendRequest) error { sent = append(sent, r); return nil },
	}
	ctx := context.Background()
	w.Sync(ctx)
	key := root + "|sales"

	// Run 1: the boss reported up itself; no fallback.
	w.Handle(ctx, key, orgbridge.Event{Type: "xorg", Run: "r1", From: "sales:lead", To: "hq:ceo"})
	w.Handle(ctx, key, orgbridge.Event{Type: "status", Run: "r1", Msg: "org stopped"})
	if len(sent) != 0 {
		t.Fatalf("fallback sent although the boss reported: %+v", sent)
	}
	// Run 2: silent; one fallback, even if the stop is seen twice.
	w.Handle(ctx, key, orgbridge.Event{Type: "status", Run: "r2", Msg: "org stopped"})
	w.Handle(ctx, key, orgbridge.Event{Type: "status", Run: "r2", Msg: "org stopped"})
	if len(sent) != 1 || sent[0].Org != "hq" || sent[0].To != "ceo" || sent[0].From != "sales:lead" || !strings.Contains(sent[0].Body, "done") {
		t.Fatalf("fallback = %+v", sent)
	}
}

// decisionsDB returns a database holding one org_decisions row per
// (org, run, cost) triple in rows.
func decisionsDB(t *testing.T, rows []struct {
	Org  string
	Run  string
	Cost float64
}) *sql.DB {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "group.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if _, err := db.DB.Exec(
			`INSERT INTO org_decisions (id, profile_id, org_name, run_id, item_kind, item_ref, item_hash, class, tier, level, resolver, verdict, cost_usd, created_at)
			 VALUES (?, 'p', ?, ?, 'approval', 'ref', 'h', 'c', 'consequential', 'full', 'model:test', 'approved', ?, '2026-09-18T00:00:00Z')`,
			fmt.Sprintf("d%d", i), r.Org, r.Run, r.Cost); err != nil {
			t.Fatal(err)
		}
	}
	return db.DB
}

// The group budget is per run: decider spend from earlier runs must not
// be rolled up, or a group is refused forever once its lifetime decider
// spend passes the ceiling.
func TestRollupCountsOnlyThisRunsDeciderSpend(t *testing.T) {
	root := t.TempDir()
	writeGroup(t, root)
	db := decisionsDB(t, []struct {
		Org  string
		Run  string
		Cost float64
	}{
		{"sales", "run-sales", 1},   // this run
		{"sales", "run-older", 100}, // a finished run
		{"hq", "run-hq", 2},
	})
	env := costsEnv(root, map[string]string{"sales": `{"items":[{"role":"lead","cost_usd":1}]}`})
	env.DB = db
	st, err := env.GroupStatus(context.Background(), "hq")
	if err != nil {
		t.Fatal(err)
	}
	if st.HolderUS != 2 || st.Children[0].CostUSD != 2 || st.RollupUS != 4 {
		t.Fatalf("status = %+v", st)
	}
	if err := env.CheckStart(context.Background(), "hq", "sales"); err != nil {
		t.Fatalf("a fresh run refused over a finished run's decider spend: %v", err)
	}
}
