package orgdesign

import (
	"encoding/json"
	"strings"
	"testing"
)

func newTestDoc() *Doc {
	return &Doc{
		Name: "x", Goal: "g", Roles: []Role{
			{ID: "lead", Title: "Lead", Type: "boss", ReportsTo: nil, Responsibilities: []string{}},
			{ID: "a", Title: "A", Type: "specialist", ReportsTo: strPtr("lead"), Responsibilities: []string{}},
			{ID: "b", Title: "B", Type: "specialist", ReportsTo: strPtr("a"), Responsibilities: []string{}},
			{ID: "c", Title: "C", Type: "specialist", ReportsTo: strPtr("a"), Responsibilities: []string{}},
		},
	}
}

func TestAddRole_DerivesID(t *testing.T) {
	d := newTestDoc()
	r, err := d.AddRole(Role{Title: "Security Auditor", ReportsTo: strPtr("lead")})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "security-auditor" {
		t.Errorf("derived id = %q", r.ID)
	}
	if r.Type != "specialist" {
		t.Errorf("default type = %q, want specialist", r.Type)
	}
}

func TestAddRole_RejectsDuplicateExplicitID(t *testing.T) {
	d := newTestDoc()
	if _, err := d.AddRole(Role{ID: "a", Title: "Dup"}); err == nil {
		t.Fatal("expected error for duplicate explicit id")
	}
}

func TestSetReportsTo_RejectsCycle(t *testing.T) {
	d := newTestDoc()
	// b currently reports to a. Making "a" report to "b" would close a cycle.
	if err := d.SetReportsTo("a", "b"); err == nil {
		t.Fatal("expected cycle rejection")
	}
}

func TestSetReportsTo_ReplaceSemantics(t *testing.T) {
	d := newTestDoc()
	if err := d.SetReportsTo("b", "lead"); err != nil {
		t.Fatal(err)
	}
	r, _ := d.FindRole("b")
	if *r.ReportsTo != "lead" {
		t.Errorf("b.ReportsTo = %v, want lead", *r.ReportsTo)
	}
	// a should still have only c as a child now (b moved away).
	children := d.Children("a")
	if len(children) != 1 || children[0].ID != "c" {
		t.Errorf("a's children after move = %v", children)
	}
}

func TestSetReportsTo_SecondRootRejected(t *testing.T) {
	d := newTestDoc()
	if err := d.SetReportsTo("a", ""); err == nil {
		t.Fatal("expected error: org already has root 'lead'")
	}
}

func TestRemoveRole_ReparentToGrandparent(t *testing.T) {
	d := newTestDoc()
	removed, err := d.RemoveRole("a", Reparent)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "a" {
		t.Fatalf("removed = %v", removed)
	}
	b, _ := d.FindRole("b")
	c, _ := d.FindRole("c")
	if *b.ReportsTo != "lead" || *c.ReportsTo != "lead" {
		t.Errorf("children not reparented to grandparent: b=%v c=%v", *b.ReportsTo, *c.ReportsTo)
	}
	if err := Validate(d); err != nil {
		t.Errorf("doc invalid after reparent-delete: %v", err)
	}
}

func TestRemoveRole_Cascade(t *testing.T) {
	d := newTestDoc()
	removed, err := d.RemoveRole("a", Cascade)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("expected 3 removed (a, b, c), got %v", removed)
	}
	if len(d.Roles) != 1 {
		t.Fatalf("expected only lead left, got %d roles", len(d.Roles))
	}
}

func TestRemoveRole_RootWithMultipleChildrenRefused(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "lead", ReportsTo: nil},
		{ID: "child1", ReportsTo: strPtr("lead")},
		{ID: "child2", ReportsTo: strPtr("lead")},
	}}
	if _, err := d.RemoveRole("lead", Reparent); err == nil {
		t.Fatal("expected refusal: root has multiple direct children")
	}
}

func TestRemoveRole_RootWithSingleChildPromotesIt(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{
		{ID: "lead", ReportsTo: nil},
		{ID: "only-child", ReportsTo: strPtr("lead")},
	}}
	removed, err := d.RemoveRole("lead", Reparent)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v", removed)
	}
	child, _ := d.FindRole("only-child")
	if child.ReportsTo != nil {
		t.Errorf("only-child should have become the new root, got ReportsTo=%v", child.ReportsTo)
	}
	if err := Validate(d); err != nil {
		t.Errorf("doc invalid: %v", err)
	}
}

func TestRemoveRole_LastRoleRefused(t *testing.T) {
	d := &Doc{Name: "x", Roles: []Role{{ID: "solo", ReportsTo: nil}}}
	if _, err := d.RemoveRole("solo", Reparent); err == nil {
		t.Fatal("expected refusal: cannot remove the last role")
	}
}

func TestUpdateRole_PatchSemantics(t *testing.T) {
	d := newTestDoc()
	title := "New Title"
	model := "claude-opus-5"
	if _, err := d.UpdateRole("a", RolePatch{Title: &title, Model: &model}); err != nil {
		t.Fatal(err)
	}
	r, _ := d.FindRole("a")
	if r.Title != "New Title" {
		t.Errorf("title not patched: %q", r.Title)
	}
	if r.Type != "specialist" {
		t.Errorf("type should be unchanged, got %q", r.Type)
	}
	var m string
	_ = m
	// Model clearing:
	empty := ""
	if _, err := d.UpdateRole("a", RolePatch{Model: &empty}); err != nil {
		t.Fatal(err)
	}
	r, _ = d.FindRole("a")
	if _, ok := r.AdapterConfig["model"]; ok {
		t.Error("model should have been cleared")
	}
}

func TestPromoteToRoot_DirectChild(t *testing.T) {
	d := newTestDoc()
	if err := d.PromoteToRoot("a"); err != nil {
		t.Fatal(err)
	}
	a, _ := d.FindRole("a")
	lead, _ := d.FindRole("lead")
	if a.ReportsTo != nil {
		t.Errorf("a should be root, ReportsTo=%v", a.ReportsTo)
	}
	if a.Type != "boss" {
		t.Errorf("a.Type = %q, want boss", a.Type)
	}
	if lead.ReportsTo == nil || *lead.ReportsTo != "a" {
		t.Errorf("lead should now report to a, got %v", lead.ReportsTo)
	}
	if lead.Type != "specialist" {
		t.Errorf("lead.Type = %q, want specialist (downgraded from boss)", lead.Type)
	}
	if err := Validate(d); err != nil {
		t.Errorf("doc invalid after promote: %v", err)
	}
}

func TestPromoteToRoot_DeepDescendant_ReversesWholePath(t *testing.T) {
	// lead(root,boss) -> a -> {b, c}. Promote b: expect b(root)->a->lead,
	// with c (off the path) left pointing at "a" unchanged.
	d := newTestDoc()
	if err := d.PromoteToRoot("b"); err != nil {
		t.Fatal(err)
	}
	b, _ := d.FindRole("b")
	a, _ := d.FindRole("a")
	lead, _ := d.FindRole("lead")
	c, _ := d.FindRole("c")
	if b.ReportsTo != nil || b.Type != "boss" {
		t.Errorf("b should be the new root/boss, got ReportsTo=%v Type=%q", b.ReportsTo, b.Type)
	}
	if a.ReportsTo == nil || *a.ReportsTo != "b" {
		t.Errorf("a should now report to b, got %v", a.ReportsTo)
	}
	if lead.ReportsTo == nil || *lead.ReportsTo != "a" {
		t.Errorf("lead should now report to a, got %v", lead.ReportsTo)
	}
	if lead.Type != "specialist" {
		t.Errorf("lead.Type = %q, want specialist (downgraded from boss)", lead.Type)
	}
	if c.ReportsTo == nil || *c.ReportsTo != "a" {
		t.Errorf("c (off the promoted path) should still report to a, got %v", c.ReportsTo)
	}
	if err := Validate(d); err != nil {
		t.Errorf("doc invalid after promote: %v", err)
	}
}

func TestPromoteToRoot_AlreadyRootRejected(t *testing.T) {
	d := newTestDoc()
	if err := d.PromoteToRoot("lead"); err == nil {
		t.Fatal("expected error: lead is already root")
	}
}

func TestPromoteToRoot_UnknownRoleRejected(t *testing.T) {
	d := newTestDoc()
	if err := d.PromoteToRoot("nope"); err == nil {
		t.Fatal("expected error: unknown role")
	}
}

func TestUpdateRole_BossGuardRejectsOnNonRoot(t *testing.T) {
	d := newTestDoc()
	boss := "boss"
	if _, err := d.UpdateRole("a", RolePatch{Type: &boss}); err == nil {
		t.Fatal("expected error: cannot patch type=boss onto a non-root role")
	}
	r, _ := d.FindRole("a")
	if r.Type != "specialist" {
		t.Errorf("a.Type should be unchanged after rejected patch, got %q", r.Type)
	}
}

func TestUpdateRole_BossGuardAllowsOnRoot(t *testing.T) {
	d := newTestDoc()
	boss := "boss"
	if _, err := d.UpdateRole("lead", RolePatch{Type: &boss}); err != nil {
		t.Fatalf("patching type=boss onto the actual root should succeed: %v", err)
	}
}

func TestAddRole_DowngradesBossTypeOnNonRoot(t *testing.T) {
	d := newTestDoc()
	r, err := d.AddRole(Role{Title: "New Manager", Type: "boss", ReportsTo: strPtr("lead")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != "specialist" {
		t.Errorf("non-root add with Type=boss should be downgraded, got %q", r.Type)
	}
}

// rolePolicyFileWrite unmarshals r.Extra["policy"].fileWrite, or returns
// (nil, false) if no policy is set.
func rolePolicyFileWrite(t *testing.T, r *Role) ([]string, bool) {
	t.Helper()
	raw, ok := r.Extra["policy"]
	if !ok {
		return nil, false
	}
	var p struct {
		FileWrite []string `json:"fileWrite"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}
	return p.FileWrite, true
}

func TestAddRole_RestrictFileWriteTrueSetsTaxonomyPolicyAndResponsibility(t *testing.T) {
	d := newTestDoc()
	r, err := d.AddRole(Role{Title: "New Hire", ReportsTo: strPtr("lead")}, true)
	if err != nil {
		t.Fatal(err)
	}
	globs, ok := rolePolicyFileWrite(t, r)
	if !ok {
		t.Fatal("expected Extra[\"policy\"].fileWrite to be set")
	}
	wantSuffixes := []string{"docs/**", "workingdocs/**", "assets/**", "codes/**", "*"}
	for _, want := range wantSuffixes {
		found := false
		for _, g := range globs {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fileWrite globs %v missing %q", globs, want)
		}
	}
	joined := strings.Join(r.Responsibilities, " | ")
	if !strings.Contains(joined, "docs") || !strings.Contains(strings.ToLower(joined), "dot-prefixed") {
		t.Errorf("Responsibilities should mention the taxonomy and dot-folder prohibition, got %v", r.Responsibilities)
	}
}

func TestAddRole_RestrictFileWriteFalseSetsNoPolicyOrResponsibility(t *testing.T) {
	d := newTestDoc()
	r, err := d.AddRole(Role{Title: "New Hire", ReportsTo: strPtr("lead")}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rolePolicyFileWrite(t, r); ok {
		t.Error("restrictFileWrite=false should leave Extra[\"policy\"] unset")
	}
	// A custom-root_dir profile (restrictFileWrite=false) is explicitly an
	// existing coding project (see the plan's correction) -- telling its
	// agent to "organize new files under docs/, workingdocs/, ..." would be
	// actively wrong guidance for that case (steers it away from src/,
	// go.mod, etc.), so the advisory text must be gated the same as the
	// enforced policy, not appended unconditionally.
	joined := strings.Join(r.Responsibilities, " | ")
	if strings.Contains(joined, "docs") {
		t.Errorf("restrictFileWrite=false should NOT append taxonomy guidance either, got %v", r.Responsibilities)
	}
}

func TestAddRole_OmittedRestrictFileWriteDefaultsToFalse(t *testing.T) {
	// Existing call sites (pre-dating this feature) call AddRole with no
	// trailing bool at all -- must keep compiling and behaving as
	// unrestricted, matching pre-feature behavior exactly.
	d := newTestDoc()
	r, err := d.AddRole(Role{Title: "New Hire", ReportsTo: strPtr("lead")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rolePolicyFileWrite(t, r); ok {
		t.Error("AddRole with no restrictFileWrite argument should leave Extra[\"policy\"] unset")
	}
}

func TestAddRole_NeverOverridesExplicitCallerPolicy(t *testing.T) {
	d := newTestDoc()
	customPolicy := json.RawMessage(`{"fileWrite":["**"]}`)
	r, err := d.AddRole(Role{
		Title:     "New Hire",
		ReportsTo: strPtr("lead"),
		Extra:     map[string]json.RawMessage{"policy": customPolicy},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	globs, ok := rolePolicyFileWrite(t, r)
	if !ok || len(globs) != 1 || globs[0] != "**" {
		t.Errorf("explicit caller policy should never be overridden by the default, got %v (ok=%v)", globs, ok)
	}
}

func TestNewOrg_RootRoleGetsSameDefaultsAsAddRole(t *testing.T) {
	d := NewOrg("test-org", "test goal", NewOrgOptions{RestrictFileWrite: true})
	root, ok := d.RootRole()
	if !ok {
		t.Fatal("expected a root role")
	}
	if _, ok := rolePolicyFileWrite(t, root); !ok {
		t.Error("NewOrg's root role should get the same default fileWrite policy as AddRole(..., true)")
	}
	joined := strings.Join(root.Responsibilities, " | ")
	if !strings.Contains(joined, "docs") {
		t.Errorf("NewOrg's root role should get the taxonomy responsibility too, got %v", root.Responsibilities)
	}
}

func TestNewOrg_RestrictFileWriteFalseIsDefaultZeroValue(t *testing.T) {
	// NewOrgOptions{} (zero value) must behave exactly as it did before
	// this feature existed -- no fileWrite policy on the root role.
	d := NewOrg("test-org", "test goal", NewOrgOptions{})
	root, ok := d.RootRole()
	if !ok {
		t.Fatal("expected a root role")
	}
	if _, ok := rolePolicyFileWrite(t, root); ok {
		t.Error("NewOrgOptions{} zero value should leave the root role's policy unset")
	}
}

func TestSetLayout_SkipsUnknownIDs(t *testing.T) {
	d := newTestDoc()
	err := d.SetLayout(map[string]RoleUI{
		"a":       {X: 10, Y: 20, Icon: "coder"},
		"unknown": {X: 5, Y: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := d.FindRole("a")
	if r.UI == nil || r.UI.X != 10 || r.UI.Icon != "coder" {
		t.Errorf("layout not applied: %+v", r.UI)
	}
}
