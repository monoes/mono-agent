package orgdesign

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUpdateSection(t *testing.T) {
	d := fixtureDoc(t)
	usd, rounds := 9.0, 3
	w := []string{"docs/**"}
	lead := "coder"
	if err := d.UpdateSection("development", SectionPatch{Lead: &lead, BudgetUSD: &usd, Writes: &w, MaxReworkRounds: &rounds}); err != nil {
		t.Fatal(err)
	}
	s := d.Sections.Find("development")
	if s.Lead != "coder" || *s.Budget.USD != 9 || s.Writes[0] != "docs/**" || *s.MaxReworkRounds != 3 {
		t.Fatalf("patch not applied: %+v", s)
	}
	if err := d.UpdateSection("development", SectionPatch{ClearBudget: true, ClearMaxRework: true}); err != nil {
		t.Fatal(err)
	}
	if s.Budget != nil || s.MaxReworkRounds != nil {
		t.Fatal("clear flags did not clear")
	}
	bad := "boss"
	before := snapshot(t, d)
	if err := d.UpdateSection("development", SectionPatch{Lead: &bad}); err == nil {
		t.Fatal("the root cannot lead a section")
	}
	zero := 0.0
	if err := d.UpdateSection("development", SectionPatch{BudgetUSD: &zero}); err == nil {
		t.Fatal("a zero budget must be refused")
	}
	if snapshot(t, d) != before {
		t.Fatal("refused patches changed the doc")
	}
	mustValid(t, d)
}

func TestNewSectionFromMovesRoles(t *testing.T) {
	d := fixtureDoc(t)
	if err := d.NewSectionFrom("review", []string{"coder"}); err != nil {
		t.Fatal(err)
	}
	if d.SectionOf("coder") != "review" || containsString(d.Sections.Find("development").Members, "coder") {
		t.Fatal("coder not moved")
	}
	mustValid(t, d)
	if err := d.NewSectionFrom("emptied", []string{"qa-lead"}); err == nil {
		t.Fatal("moving the only member out must be refused")
	}
	if err := d.NewSectionFrom("Bad Name", []string{"coder"}); err == nil {
		t.Fatal("bad name")
	}
}

func TestNewSectionFromPlainOrgTakesEveryRole(t *testing.T) {
	d := fixtureDoc(t)
	d.Sections, d.Documents = nil, nil
	if err := d.NewSectionFrom("all", []string{"coder"}); err != nil {
		t.Fatal(err)
	}
	s := d.Sections.Find("all")
	if len(s.Members) != 3 || s.Lead != "coder" {
		t.Fatalf("members=%v lead=%q", s.Members, s.Lead)
	}
	mustValid(t, d)
}

func TestDocumentEdges(t *testing.T) {
	d := fixtureDoc(t)
	if err := d.AddDocumentEdge("qa", "development", "bug"); err != nil {
		t.Fatal(err)
	}
	if d.Documents.Find("bug") == nil {
		t.Fatal("type not declared")
	}
	mustValid(t, d)
	if err := d.AddDocumentEdge("qa", "development", "bug"); err == nil {
		t.Fatal("duplicate edge must be refused")
	}
	if err := d.AddDocumentEdge("development", "qa", "report"); err == nil || !strings.Contains(err.Error(), "consume") {
		t.Fatalf("consume+publish of one type must be refused: %v", err)
	}
	if err := d.AddDocumentEdge("qa", "qa", "x"); err == nil {
		t.Fatal("self edge")
	}
	if err := d.RemoveDocumentEdge("qa", "development", "bug"); err != nil {
		t.Fatal(err)
	}
	if d.Documents.Find("bug") != nil {
		t.Fatal("unused default type should be dropped")
	}
	if err := d.RemoveDocumentEdge("qa", "development", "report"); err != nil {
		t.Fatal(err)
	}
	if d.Documents.Find("report") == nil {
		t.Fatal("a customised document must stay declared")
	}
}

func TestCrossSectionMessageLinkRefused(t *testing.T) {
	d := fixtureDoc(t)
	d.AddRoleToSection(Role{ID: "q2", Title: "Q2", Type: "specialist", ReportsTo: strPtr("qa-lead")}, "qa")
	before := snapshot(t, d)
	err := d.SetReportsTo("q2", "coder")
	if err == nil || !strings.Contains(err.Error(), "document") {
		t.Fatalf("direct cross-section link must be refused with the reason, got %v", err)
	}
	if _, err := d.AddRoleToSection(Role{ID: "x", Title: "X", Type: "specialist", ReportsTo: strPtr("coder")}, "qa"); err == nil {
		t.Fatal("new role reporting into another section must be refused")
	}
	if snapshot(t, d) != before {
		t.Fatal("refusals changed the doc")
	}
	if err := d.AssignRole("q2", "development"); err != nil {
		t.Fatal(err)
	}
	if r, _ := d.FindRole("q2"); *r.ReportsTo != "dev-lead" {
		t.Fatalf("moved role should report to its new lead, reports to %v", *r.ReportsTo)
	}
	mustValid(t, d)
}

func TestSectionsOrgDeclaresDocumentsAndAgentCap(t *testing.T) {
	d := fixtureDoc(t)
	d.Sections, d.Documents = nil, nil
	d.RunConfig["max_concurrent_agents"] = json.RawMessage("4")
	for _, id := range []string{"x1", "x2", "x3"} {
		d.Roles = append(d.Roles, Role{ID: id, Title: id, Type: "specialist", ReportsTo: strPtr("boss")})
	}
	if err := d.NewSectionFrom("all", []string{"coder"}); err != nil {
		t.Fatal(err)
	}
	b := snapshot(t, d)
	if !strings.Contains(b, `"documents":{}`) {
		t.Fatalf("a sections org must carry a documents map: %s", b)
	}
	if !strings.Contains(b, `"max_concurrent_agents":7`) {
		t.Fatalf("agent cap not raised to the 7 roles: %s", b)
	}
	if _, err := d.AddRoleToSection(Role{ID: "x4", Title: "x4", Type: "specialist", ReportsTo: strPtr("coder")}, "all"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot(t, d), `"max_concurrent_agents":8`) {
		t.Fatal("cap not raised for the added role")
	}
}
