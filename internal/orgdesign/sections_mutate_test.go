package orgdesign

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func fixtureDoc(t *testing.T) *Doc {
	t.Helper()
	var d Doc
	if err := json.Unmarshal([]byte(sectionsOrg), &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

func mustValid(t *testing.T, d *Doc) {
	t.Helper()
	if err := Validate(d); err != nil {
		t.Fatalf("invariant broken: %v", err)
	}
}

func snapshot(t *testing.T, d *Doc) string {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAddRoleJoinsParentsSection(t *testing.T) {
	d := fixtureDoc(t)
	r, err := d.AddRole(Role{ID: "tester", Title: "Tester", Type: "specialist", ReportsTo: strPtr("dev-lead")})
	if err != nil || r.ID != "tester" {
		t.Fatalf("%v", err)
	}
	if d.SectionOf("tester") != "development" {
		t.Fatalf("new role must join its parent's section, in %q", d.SectionOf("tester"))
	}
	mustValid(t, d)
}

func TestAddRoleUnderRootNeedsASection(t *testing.T) {
	d := fixtureDoc(t)
	before := snapshot(t, d)
	if _, err := d.AddRole(Role{ID: "orphan", Title: "O", Type: "specialist", ReportsTo: strPtr("boss")}); err == nil {
		t.Fatal("a role under the root must be refused in a sections org (it would sit in no section)")
	}
	if snapshot(t, d) != before {
		t.Fatal("a refused add must change nothing")
	}
	if _, err := d.AddRoleToSection(Role{ID: "pm", Title: "PM", Type: "specialist", ReportsTo: strPtr("boss")}, "qa"); err != nil {
		t.Fatal(err)
	}
	if d.SectionOf("pm") != "qa" {
		t.Fatal("AddRoleToSection did not place the role")
	}
	if _, err := d.AddRoleToSection(Role{ID: "x", ReportsTo: strPtr("boss")}, "nope"); err == nil {
		t.Fatal("unknown section must be refused")
	}
	mustValid(t, d)
}

func TestAddRoleSecondMemberWritesDownTheLead(t *testing.T) {
	d := fixtureDoc(t)
	qa := d.Sections.Find("qa")
	qa.Lead = ""
	if _, err := d.AddRole(Role{ID: "qa-2", Title: "QA 2", Type: "specialist", ReportsTo: strPtr("qa-lead")}); err != nil {
		t.Fatal(err)
	}
	if qa.Lead != "qa-lead" {
		t.Fatalf("lead = %q: a section of two must name its lead", qa.Lead)
	}
	mustValid(t, d)
}

func TestAddRoleOnPlainOrgIsUnchanged(t *testing.T) {
	d := NewOrg("plain", "g", NewOrgOptions{})
	root, _ := d.RootRole()
	if _, err := d.AddRole(Role{ID: "w", Title: "W", Type: "specialist", ReportsTo: strPtr(root.ID)}); err != nil {
		t.Fatal(err)
	}
	if len(d.Sections) != 0 {
		t.Fatal("a plain org must not grow sections")
	}
}

func TestRemoveRoleElectsANewLeadAndNeverOrphansOne(t *testing.T) {
	d := fixtureDoc(t)
	if _, err := d.RemoveRole("dev-lead", Reparent); err != nil {
		t.Fatal(err)
	}
	dev := d.Sections.Find("development")
	if dev.Lead != "coder" || !reflect.DeepEqual(dev.Members, []string{"coder"}) {
		t.Fatalf("lead %q members %v: the remaining member must lead", dev.Lead, dev.Members)
	}
	mustValid(t, d)
}

func TestRemoveLastMemberRefusedAtomically(t *testing.T) {
	d := fixtureDoc(t)
	before := snapshot(t, d)
	if _, err := d.RemoveRole("qa-lead", Reparent); err == nil {
		t.Fatal("removing a section's only member must be refused")
	}
	if _, err := d.RemoveRole("dev-lead", Cascade); err == nil {
		t.Fatal("a cascade that empties a section must be refused")
	}
	if snapshot(t, d) != before {
		t.Fatal("a refused removal must change nothing")
	}
	// Once the section is deleted its roles move, and removal works.
	if err := d.DeleteSection("qa", "development"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RemoveRole("qa-lead", Reparent); err != nil {
		t.Fatal(err)
	}
	mustValid(t, d)
}

func TestRemoveRootOfSectionsOrgRefused(t *testing.T) {
	d := fixtureDoc(t)
	if _, err := d.RemoveRole("boss", Reparent); err == nil {
		t.Fatal("removing the root must be refused")
	}
	if err := d.PromoteToRoot("coder"); err == nil {
		t.Fatal("promoting a section member to root must be refused")
	}
	if err := d.SetReportsTo("coder", ""); err == nil {
		t.Fatal("making a section member a root must be refused")
	}
}

func TestSectionOps(t *testing.T) {
	d := fixtureDoc(t)
	if err := d.AssignRole("coder", "qa"); err != nil {
		t.Fatal(err)
	}
	if d.SectionOf("coder") != "qa" || containsString(d.Sections.Find("development").Members, "coder") {
		t.Fatal("AssignRole did not move the role")
	}
	if err := d.AssignRole("dev-lead", "qa"); err == nil {
		t.Fatal("moving the only member out of a section must be refused")
	}
	if err := d.AssignRole("boss", "qa"); err == nil {
		t.Fatal("the root cannot join a section")
	}
	if err := d.SetSectionLead("qa", "coder"); err != nil {
		t.Fatal(err)
	}
	if err := d.AddSection("Bad Name", Section{Members: []string{"x"}}); err == nil {
		t.Fatal("invalid section name accepted")
	}
	if err := d.AddSection("ops", Section{Members: []string{"coder"}}); err == nil {
		t.Fatal("a role already in a section must be refused")
	}
	if _, err := d.AddRoleToSection(Role{ID: "sre", Title: "SRE", Type: "specialist", ReportsTo: strPtr("boss")}, "qa"); err != nil {
		t.Fatal(err)
	}
	if err := d.AssignRole("sre", "development"); err != nil {
		t.Fatal(err)
	}
	if err := d.AddSection("ops", Section{Members: []string{}}); err == nil {
		t.Fatal("a section needs a member")
	}
	if err := d.DeleteSection("development", ""); err == nil {
		t.Fatal("deleting a section with roles needs a destination")
	}
	if err := d.DeleteSection("development", "qa"); err != nil {
		t.Fatal(err)
	}
	if d.SectionOf("sre") != "qa" || d.Sections.Find("development") != nil {
		t.Fatal("DeleteSection did not move the roster")
	}
	mustValid(t, d)
}

func TestValidateSectionsInvariant(t *testing.T) {
	cases := map[string]func(*Doc){
		"role outside every section": func(d *Doc) {
			d.Sections.Find("development").Members = []string{"dev-lead"}
		},
		"is the root": func(d *Doc) {
			s := d.Sections.Find("qa")
			s.Members = append(s.Members, "boss")
		},
		"in sections": func(d *Doc) {
			s := d.Sections.Find("qa")
			s.Members = append(s.Members, "coder")
		},
		"does not exist": func(d *Doc) {
			s := d.Sections.Find("qa")
			s.Members = append(s.Members, "ghost")
		},
		"must name its lead": func(d *Doc) {
			d.Sections.Find("development").Lead = ""
		},
	}
	for want, mutate := range cases {
		d := fixtureDoc(t)
		mutate(d)
		err := Validate(d)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v", want, err)
		}
	}
}

func TestSetSectionLeadRetainsDedicatedFormerLead(t *testing.T) {
	d := fixtureDoc(t)
	section := d.Sections.Find("development")
	section.Members = []string{"coder"}
	mustValid(t, d)
	if err := d.SetSectionLead("development", "coder"); err != nil {
		t.Fatal(err)
	}
	if section.Lead != "coder" || !reflect.DeepEqual(section.Members, []string{"coder", "dev-lead"}) {
		t.Fatalf("section = %+v", section)
	}
	mustValid(t, d)
}
