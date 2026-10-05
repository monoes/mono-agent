package orgdesign

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/orgsign/orgsigntest"
)

// sectionsOrg is a sections org in the layout monomind writes (JSON.stringify
// 2-space, its own key order — not sorted, not struct order), shaped after
// monomind's dev-qa test org (__tests__/orgrt/support/dev-qa-defs.ts) plus the
// legacy and unknown keys a real file can carry.
const sectionsOrg = `{
  "name": "sec-org",
  "goal": "ship <fast> & safe",
  "requires": {
    "sections": 1
  },
  "status": "stopped",
  "schedule": null,
  "run_config": {
    "max_concurrent_agents": 20,
    "budget_usd": 30.0,
    "completion": {"mode": "dag", "protocol": "sections-v1"}
  },
  "roles": [
    {
      "id": "boss",
      "title": "Boss",
      "type": "boss",
      "reports_to": null,
      "responsibilities": ["Lead the org."],
      "policy": {"sandbox": {"mode": "off"}}
    },
    {
      "id": "dev-lead",
      "title": "Dev Lead",
      "type": "specialist",
      "reports_to": "boss",
      "responsibilities": ["Lead development."],
      "budget_usd": 3,
      "policy": {"sandbox": {"mode": "off"}}
    },
    {
      "id": "coder",
      "title": "Coder",
      "type": "specialist",
      "reports_to": "dev-lead",
      "responsibilities": ["Write code."],
      "budget_usd": 3.5,
      "policy": {"sandbox": {"mode": "off"}}
    },
    {
      "id": "qa-lead",
      "title": "QA Lead",
      "type": "specialist",
      "reports_to": "boss",
      "responsibilities": ["Test."],
      "budget_usd": 4,
      "policy": {"sandbox": {"mode": "off"}}
    }
  ],
  "sections": {
    "qa": {
      "lead": "qa-lead",
      "members": ["qa-lead"],
      "publishes": ["report"],
      "consumes": ["build"],
      "budget": {"usd": 5}
    },
    "development": {
      "members": ["dev-lead", "coder"],
      "lead": "dev-lead",
      "mode": "execution",
      "requests": "via-lead",
      "publishes": ["build"],
      "consumes": ["report"],
      "writes": ["src/**"],
      "max_rework_rounds": 2,
      "parallelism": {"max_parallel": 2},
      "budget": {"usd": 6.5},
      "future_flag": {"a": [1, 2]}
    }
  },
  "documents": {
    "report": {
      "schema": {"type": "object", "required": ["summary"]},
      "evidence": [{"kind": "source", "verify": "cited"}],
      "acceptance": "each",
      "x_note": "kept"
    },
    "build": {
      "schema": {"type": "object"},
      "visibility": "consumers",
      "max_publish_attempts": 3
    }
  },
  "fence": {"k": "v"}
}
`

func loadFixture(t *testing.T, body string) (*Doc, string, string) {
	t.Helper()
	root := t.TempDir()
	path, err := ConfigPath(root, "sec-org")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return d, root, path
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSectionsTypedModel(t *testing.T) {
	d, _, _ := loadFixture(t, sectionsOrg)
	if d.Requires == nil || d.Requires.Sections == nil || *d.Requires.Sections != 1 {
		t.Fatalf("requires = %+v", d.Requires)
	}
	if got := []string{d.Sections[0].Name, d.Sections[1].Name}; got[0] != "qa" || got[1] != "development" {
		t.Fatalf("section order not kept: %v", got)
	}
	dev := d.Sections.Find("development")
	if dev.Lead != "dev-lead" || len(dev.Members) != 2 || dev.Writes[0] != "src/**" ||
		*dev.MaxReworkRounds != 2 || *dev.Parallelism.MaxParallel != 2 || *dev.Budget.USD != 6.5 ||
		dev.Mode != "execution" || dev.Requests != "via-lead" {
		t.Fatalf("development = %+v", dev)
	}
	if _, ok := dev.Extra["future_flag"]; !ok || len(dev.Extra) != 1 {
		t.Fatalf("unknown section key must land in Extra, got %v", dev.Extra)
	}
	rep := d.Documents.Find("report")
	if rep == nil || rep.Acceptance != "each" || len(rep.Evidence) != 1 || rep.Evidence[0].Kind != "source" ||
		string(rep.Extra["x_note"]) != `"kept"` || rep.Schema == nil {
		t.Fatalf("report = %+v", rep)
	}
	if d.Documents.Find("build").MaxPublishAttempts == nil {
		t.Fatal("max_publish_attempts not typed")
	}
	if _, ok := d.Extra["sections"]; ok {
		t.Fatal("sections must not also sit in Extra")
	}
	if v, ok := RoleBudgetUSD(mustRole(t, d, "coder")); !ok || v != 3.5 {
		t.Fatalf("coder cap = %v %v", v, ok)
	}
	if !d.SectionsEnabled() || d.SectionOf("coder") != "development" || d.SectionOf("boss") != "" {
		t.Fatal("section placement lookup wrong")
	}
	if err := Validate(d); err != nil {
		t.Fatalf("fixture must validate: %v", err)
	}
}

func mustRole(t *testing.T, d *Doc, id string) *Role {
	t.Helper()
	r, i := d.FindRole(id)
	if i == -1 {
		t.Fatalf("role %s missing", id)
	}
	return r
}

func TestSectionsSaveUnchangedIsByteIdentical(t *testing.T) {
	d, root, path := loadFixture(t, sectionsOrg)
	if _, err := Save(root, d); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != sectionsOrg {
		t.Fatalf("an unedited save changed the file:\n%s", firstDiff(sectionsOrg, got))
	}
}

func TestSectionsEditRoleRewritesOnlyTheEdit(t *testing.T) {
	d, root, path := loadFixture(t, sectionsOrg)
	title := "Principal Coder"
	if _, err := d.UpdateRole("coder", RolePatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(root, d); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sectionsOrg, `"title": "Coder"`, `"title": "Principal Coder"`, 1)
	if got := mustRead(t, path); got != want {
		t.Fatalf("edit leaked outside the edited line:\n%s", firstDiff(want, got))
	}
}

func TestSectionsEditSectionRewritesOnlyTheEdit(t *testing.T) {
	d, root, path := loadFixture(t, sectionsOrg)
	n := 4
	d.Sections.Find("development").MaxReworkRounds = &n
	d.Documents.Find("build").Acceptance = "each"
	if _, err := Save(root, d); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, path)
	if !strings.Contains(got, `"max_rework_rounds": 4`) || !strings.Contains(got, `"acceptance": "each"`) {
		t.Fatalf("edits missing:\n%s", got)
	}
	for _, keep := range []string{`"goal": "ship <fast> & safe"`, `"budget_usd": 30.0`, `"future_flag": {"a": [1, 2]}`, `"policy": {"sandbox": {"mode": "off"}}`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("lost verbatim text %s", keep)
		}
	}
	if strings.Index(got, `"qa"`) > strings.Index(got, `"development"`) {
		t.Fatal("section order changed")
	}
}

func TestSectionsMalformedShapesSurviveRoundTrip(t *testing.T) {
	body := `{
  "name": "sec-org",
  "goal": "g",
  "status": "stopped",
  "schedule": null,
  "requires": null,
  "sections": ["not", "an", "object"],
  "documents": {"report": "oops"},
  "roles": [
    {"id": "boss", "title": "Boss", "type": "boss", "reports_to": null, "responsibilities": []}
  ]
}
`
	d, root, path := loadFixture(t, body)
	if d.SectionsEnabled() || d.Requires != nil || len(d.Sections) != 0 {
		t.Fatal("malformed keys must stay out of the typed model")
	}
	if _, err := Save(root, d); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != body {
		t.Fatalf("malformed sections keys changed:\n%s", firstDiff(body, got))
	}
	// A typed key with a wrong sub-type keeps just that key.
	d2, _, _ := loadFixture(t, strings.Replace(sectionsOrg, `"members": ["dev-lead", "coder"]`, `"members": "dev-lead"`, 1))
	if string(d2.Sections.Find("development").Extra["members"]) != `"dev-lead"` {
		t.Fatalf("wrong-typed members must stay in Extra: %+v", d2.Sections.Find("development"))
	}
	if err := Validate(d2); err == nil {
		t.Fatal("a section with no readable members must not validate")
	}
}

func TestSectionsEmptyObjectsRoundTrip(t *testing.T) {
	body := `{
  "name": "sec-org",
  "goal": "g",
  "status": "stopped",
  "schedule": null,
  "sections": {},
  "documents": {},
  "roles": [
    {
      "id": "a",
      "title": "A",
      "type": "boss",
      "reports_to": null,
      "responsibilities": []
    }
  ]
}
`
	d, root, path := loadFixture(t, body)
	if d.SectionsEnabled() {
		t.Fatal("sections: {} must not enable the surface")
	}
	if _, err := Save(root, d); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != body {
		t.Fatalf("empty objects changed:\n%s", firstDiff(body, got))
	}
}

func TestSectionsSaveResignsOnlyExactLoadedBytes(t *testing.T) {
	orgsigntest.AsOperator(t)
	t.Setenv("MONOMIND_ORGRT_OPERATOR_DIR", filepath.Join(t.TempDir(), "operator"))
	ctx := context.Background()
	d, root, path := loadFixture(t, sectionsOrg)
	orgsigntest.Sign(t, root, "sec-org")

	// Loaded from exactly the signed bytes: the write may be re-signed.
	pre := orgsign.Before(ctx, nil, root, "sec-org", d.LoadedSHA(), false)
	if !pre.Eligible() {
		t.Fatalf("signed sections org loaded as is must be eligible: %+v", pre)
	}
	n := 5
	d.Sections.Find("development").MaxReworkRounds = &n
	sha, err := Save(root, d)
	if err != nil {
		t.Fatal(err)
	}
	if sha != d.LoadedSHA() || sha != orgsign.SHA256([]byte(mustRead(t, path))) {
		t.Fatal("Save must return the sha of the bytes it wrote, and LoadedSHA follow it")
	}

	// The file moved on after the load (a different signed version): the
	// stale Doc's write must not be re-signed.
	stale, root2, path2 := loadFixture(t, sectionsOrg)
	orgsigntest.Sign(t, root2, "sec-org")
	other := strings.Replace(sectionsOrg, `"usd": 6.5`, `"usd": 7`, 1)
	if err := os.WriteFile(path2, []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	orgsigntest.Sign(t, root2, "sec-org")
	if pre := orgsign.Before(ctx, nil, root2, "sec-org", stale.LoadedSHA(), false); pre.Eligible() {
		t.Fatal("a Doc read before the file changed must not be re-signed")
	}

	// A Doc that never came from the file carries no sha at all.
	var built Doc
	if err := json.Unmarshal([]byte(sectionsOrg), &built); err != nil {
		t.Fatal(err)
	}
	if built.LoadedSHA() != "" {
		t.Fatal("a Doc not loaded from a file must have an empty LoadedSHA")
	}
	if pre := orgsign.Before(ctx, nil, root2, "sec-org", built.LoadedSHA(), false); pre.Eligible() {
		t.Fatal("a Doc not loaded from the file must not be re-signed")
	}
}

func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return "line " + itoa(i+1) + "\nwant: " + a + "\n got: " + b
		}
	}
	return "(no difference)"
}

func itoa(i int) string { return strconv.Itoa(i) }
