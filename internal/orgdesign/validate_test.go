package orgdesign

import "testing"

func TestValidate_SingleRootOK(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", ReportsTo: nil},
		{ID: "b", ReportsTo: strPtr("a")},
	}}
	if err := Validate(d); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidate_NoRoot(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", ReportsTo: strPtr("b")},
		{ID: "b", ReportsTo: strPtr("a")},
	}}
	err := Validate(d)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidate_MultipleRoots(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", ReportsTo: nil},
		{ID: "b", ReportsTo: nil},
	}}
	if err := Validate(d); err == nil {
		t.Fatal("expected error for multiple roots")
	}
}

func TestValidate_MultipleBossRejected(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", Type: "boss", ReportsTo: nil},
		{ID: "b", Type: "boss", ReportsTo: strPtr("a")},
	}}
	if err := Validate(d); err == nil {
		t.Fatal("expected error: more than one role has type boss")
	}
}

func TestValidate_BossNotRootRejected(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", Type: "specialist", ReportsTo: nil},
		{ID: "b", Type: "boss", ReportsTo: strPtr("a")},
	}}
	if err := Validate(d); err == nil {
		t.Fatal("expected error: type=boss on a non-root role")
	}
}

func TestValidate_BossOnRootOK(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", Type: "boss", ReportsTo: nil},
		{ID: "b", Type: "specialist", ReportsTo: strPtr("a")},
	}}
	if err := Validate(d); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidate_DuplicateIDs(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", ReportsTo: nil},
		{ID: "a", ReportsTo: strPtr("a")},
	}}
	if err := Validate(d); err == nil {
		t.Fatal("expected error for duplicate ids")
	}
}

func TestValidate_UnresolvedReportsTo(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", ReportsTo: nil},
		{ID: "b", ReportsTo: strPtr("ghost")},
	}}
	if err := Validate(d); err == nil {
		t.Fatal("expected error for unresolved reports_to")
	}
}

// TestValidate_CycleWithSeparateRoot reproduces the exact case empirically
// confirmed to pass `monomind org validate` with exit 0: a valid root "a"
// coexists with an unreachable b->c->b cycle. Our Go layer must catch this
// even though monomind's own checkOrgStructure does not.
func TestValidate_CycleWithSeparateRoot(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", ReportsTo: nil},
		{ID: "b", ReportsTo: strPtr("c")},
		{ID: "c", ReportsTo: strPtr("b")},
	}}
	if err := Validate(d); err == nil {
		t.Fatal("expected cycle to be detected even with a separate valid root")
	}
	cycles := DetectCycles(d.Roles)
	if len(cycles) != 1 {
		t.Fatalf("expected exactly 1 cycle, got %d: %v", len(cycles), cycles)
	}
}

func TestDetectCycles_SelfReport(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "a", ReportsTo: nil},
		{ID: "b", ReportsTo: strPtr("b")},
	}}
	if err := Validate(d); err == nil {
		t.Fatal("expected error for self-report")
	}
}

func TestWouldCycle(t *testing.T) {
	roles := []Role{
		{ID: "a", ReportsTo: nil},
		{ID: "b", ReportsTo: strPtr("a")},
		{ID: "c", ReportsTo: strPtr("b")},
	}
	// c already reports (indirectly) to a. Making "a" report to "c" would
	// close a cycle a->c->b->a.
	if !WouldCycle(roles, "a", "c") {
		t.Error("expected WouldCycle(a, c) = true")
	}
	// b reporting to a's sibling-less nothing: fine to reassign c under a directly.
	if WouldCycle(roles, "c", "a") {
		t.Error("expected WouldCycle(c, a) = false (a is not a descendant of c)")
	}
	if !WouldCycle(roles, "a", "a") {
		t.Error("expected WouldCycle(a, a) = true (self)")
	}
}

func TestUniqueRoleID(t *testing.T) {
	d := &Doc{Roles: []Role{{ID: "security-auditor"}}}
	got := UniqueRoleID(d, "Security Auditor")
	if got != "security-auditor-2" {
		t.Errorf("UniqueRoleID collision handling = %q, want security-auditor-2", got)
	}
	fresh := UniqueRoleID(d, "Node Developer")
	if fresh != "node-developer" {
		t.Errorf("UniqueRoleID(fresh) = %q, want node-developer", fresh)
	}
	empty := UniqueRoleID(&Doc{}, "!!!")
	if empty != "role" {
		t.Errorf("UniqueRoleID(unsluggable) = %q, want role", empty)
	}
}
