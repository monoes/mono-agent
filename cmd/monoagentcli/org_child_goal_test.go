package main

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// C-36: a holding child whose goal is not message-driven gets a non-fatal
// warning from create-json, validate, and group init.
func TestChildGoalWarningSurfaces(t *testing.T) {
	f := newOrgCLIFixture(t) // seeds "growth" with the standing goal "grow"
	hqJSON := `{"name":"hq","kind":"holding","goal":"g","status":"stopped","schedule":null,"roles":[{"id":"ceo","title":"CEO","type":"boss","reports_to":null,"responsibilities":[]}],"children":[{"org":"growth","start":"on_demand"}]}`

	hasWarning := func(out map[string]interface{}) bool {
		for _, w := range toStrings(out["warnings"]) {
			if strings.Contains(w, `child org "growth"`) && strings.Contains(w, `holding org "hq"`) {
				return true
			}
		}
		return false
	}

	if out := f.mustRun(t, "create-json", "hq", "--json", hqJSON); !hasWarning(out) {
		t.Fatalf("create-json hq: no child goal warning: %v", out["warnings"])
	}
	for _, name := range []string{"hq", "growth"} {
		if out := f.mustRun(t, "validate", name); !hasWarning(out) {
			t.Fatalf("validate %s: no child goal warning: %v", name, out["warnings"])
		}
	}
	if out := f.mustRun(t, "group", "init", "hq"); !hasWarning(out) {
		t.Fatalf("group init: no child goal warning: %v", out["warnings"])
	}

	d := f.load(t)
	d.Goal = "Handle requests from hq; when idle, complete."
	if _, err := orgdesign.Save(f.root, d); err != nil {
		t.Fatal(err)
	}
	out := f.mustRun(t, "validate", "hq")
	if hasWarning(out) {
		t.Fatalf("validate hq: warning after the goal was made message-driven: %v", out["warnings"])
	}
	if _, ok := out["warnings"].([]interface{}); !ok {
		t.Fatalf("validate hq: warnings should be an empty array, got %v", out["warnings"])
	}
}
