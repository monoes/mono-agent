package automation

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.2.0", "1.10.0", -1},
		{"2.0.0", "1.99.99", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"v1.0.0", "1.0.0", 0},
		{"junk", "0.0.1", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if bumpPatch("1.2.3-rc.1") != "1.2.4" {
		t.Errorf("bumpPatch = %q", bumpPatch("1.2.3-rc.1"))
	}
}

func TestEngineSatisfied(t *testing.T) {
	cases := []struct {
		rng, ver string
		want     bool
	}{
		{">=0.68.0", "0.68.0", true},
		{">=0.68.0", "v0.67.9", false},
		{">=0.68.0", "v0.68.1-3-gabc123", true},
		{">=0.68.0 <1.0.0", "1.0.0", false},
		{"^1.2.0", "1.9.0", true},
		{"^1.2.0", "2.0.0", false},
		{"^0.2.0", "0.3.0", false},
		{"~1.2.0", "1.2.9", true},
		{"~1.2.0", "1.3.0", false},
		{"=1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		{">=99.0.0", "0.0.0-dev", true}, // dev skips
		{">=99.0.0", "dev", true},
		{"", "1.0.0", true},
	}
	for _, c := range cases {
		got, err := EngineSatisfied(c.rng, c.ver)
		if err != nil || got != c.want {
			t.Errorf("EngineSatisfied(%q,%q) = %v,%v want %v", c.rng, c.ver, got, err, c.want)
		}
	}
	if _, err := EngineSatisfied("!!1.0", "1.0.0"); err == nil {
		t.Error("bad range accepted")
	}
}

func TestValidateManifestRules(t *testing.T) {
	m := Manifest{Schema: SchemaV1, ID: "Bad_ID", Name: "x", Version: "1.0", Actions: []string{"a", "a", "../x"},
		Site: Site{Domains: []string{"ok.com", "bad domain"}}, Policy: Policy{Tier: "weird"},
		Permissions: Permissions{Scripts: []string{"x.py"}}}
	codes := map[string]bool{}
	for _, is := range validateManifest(m, SourceImported) {
		codes[is.Code] = true
	}
	for _, want := range []string{"bad_id", "bad_version", "bad_tier", "bad_domain", "duplicate_action",
		"bad_action_name", "bad_script_name", "missing_step_permissions"} {
		if !codes[want] {
			t.Errorf("missing issue %s (got %v)", want, codes)
		}
	}
	// Built-ins may leave domains and steps empty.
	b := Manifest{Schema: SchemaV1, ID: "hn", Name: "HN", Version: "1.0.0", Actions: []string{"x"}}
	if is := errorsOnly(validateManifest(b, SourceBuiltin)); len(is) != 0 {
		t.Errorf("builtin manifest errors: %+v", is)
	}
	if is := errorsOnly(validateManifest(b, SourceLocal)); len(is) != 2 {
		t.Errorf("non-builtin without domains/steps: want 2 errors, got %+v", is)
	}
}

func TestEngineMismatchIsValidationError(t *testing.T) {
	old := EngineVersion
	EngineVersion = "0.50.0"
	defer func() { EngineVersion = old }()
	p, err := OpenDir(acmeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	p.Manifest.Engine = ">=0.68.0"
	found := false
	for _, is := range Validate(p) {
		if is.Code == "engine_mismatch" {
			found = true
		}
	}
	if !found {
		t.Error("engine mismatch not reported")
	}
}
