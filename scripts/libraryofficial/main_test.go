package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/automation"
)

const repoRoot = "../.."

// Every starter org in the catalog validates, and every org JSON in
// orgtemplates/ is in the catalog.
func TestOrgTemplatesValidate(t *testing.T) {
	var cat OrgCatalog
	if err := readJSON(filepath.Join(repoRoot, "orgtemplates", "catalog.json"), &cat); err != nil {
		t.Fatal(err)
	}
	if len(cat.Items) < 2 {
		t.Fatalf("catalog has %d orgs, want at least 2", len(cat.Items))
	}
	listed := map[string]bool{}
	for _, c := range cat.Items {
		listed[c.Slug] = true
		d, _, err := LoadOrgTemplate(repoRoot, c.Slug)
		if err != nil {
			t.Error(err)
			continue
		}
		if len(d.Roles) < 2 || c.Name == "" || c.Description == "" || c.Version == "" {
			t.Errorf("%s: roles=%d name=%q description=%q version=%q", c.Slug, len(d.Roles), c.Name, c.Description, c.Version)
		}
	}
	files, _ := filepath.Glob(filepath.Join(repoRoot, "orgtemplates", "*.json"))
	for _, f := range files {
		slug := strings.TrimSuffix(filepath.Base(f), ".json")
		if slug != "catalog" && !listed[slug] {
			t.Errorf("orgtemplates/%s.json is not in catalog.json", slug)
		}
	}
}

// Build writes every artifact and a manifest whose hashes match the files.
func TestBuildManifest(t *testing.T) {
	out := t.TempDir()
	for _, id := range officialAutomations {
		f, err := os.Create(filepath.Join(out, id+".mpkg"))
		if err != nil {
			t.Fatal(err)
		}
		if err := automation.Pack(filepath.Join(repoRoot, "automations", id), f); err != nil {
			t.Fatalf("pack %s: %v", id, err)
		}
		f.Close()
	}
	m, err := Build(repoRoot, out)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, it := range m.Items {
		kinds[it.Kind]++
		var got Item = it
		if err := hashInto(&got, filepath.Join(out, it.File)); err != nil || got.SHA256 != it.SHA256 {
			t.Errorf("%s: hash mismatch or missing (%v)", it.File, err)
		}
		if it.Visibility != "official" || it.Slug == "" || it.Name == "" || it.Version == "" {
			t.Errorf("incomplete item %+v", it)
		}
	}
	if kinds["automation"] != 7 || kinds["workflow"] != 4 || kinds["org"] < 2 {
		t.Fatalf("items per kind = %v", kinds)
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}
