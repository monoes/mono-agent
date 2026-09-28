package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/automations"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/health"
)

// withoutTestSeed runs the CLI the way a release build does: nothing is
// seeded into a home.
func withoutTestSeed(t *testing.T) {
	t.Helper()
	prev := automation.TestSeed
	automation.TestSeed = nil
	t.Cleanup(func() { automation.TestSeed = prev })
}

type listedAutomation struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
}

// A fresh home gets no automation packages: the official ones come from
// monoes.me (`library install automation <id>`).
func TestAutomationFreshHomeHasNoBuiltins(t *testing.T) {
	withoutTestSeed(t)
	home := t.TempDir()
	var got struct {
		Automations []listedAutomation `json:"automations"`
	}
	mustJSON(t, home, &got, "automation", "list")
	if len(got.Automations) != 0 {
		t.Fatalf("fresh home lists %d automations, want none: %+v", len(got.Automations), got.Automations)
	}
}

// Packages a previous version seeded stay installed, enabled and usable
// after upgrading to a build that seeds nothing.
func TestAutomationPreviouslySeededBuiltinsSurvive(t *testing.T) {
	withoutTestSeed(t)
	home := t.TempDir()
	reg, err := automation.Open(filepath.Join(home, ".monoagent"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Seed(automations.FS()); err != nil { // what the old binary did
		t.Fatal(err)
	}
	var got struct {
		Automations []listedAutomation `json:"automations"`
	}
	mustJSON(t, home, &got, "automation", "list")
	byID := map[string]listedAutomation{}
	for _, a := range got.Automations {
		byID[a.ID] = a
	}
	for _, id := range automations.IDs {
		a, ok := byID[id]
		if !ok || a.Source != automation.SourceBuiltin || !a.Enabled {
			t.Errorf("%s after upgrade = %+v (present %v), want an enabled built-in", id, a, ok)
		}
	}
	if a := byID["gemini"]; !a.Available { // standard tier: available in every build
		t.Errorf("gemini unavailable after upgrade: %+v", a)
	}
}

// The CLI binary must not carry the official packages: they are published
// on monoes.me, and the repo copy is for tests and the release tooling.
func TestCLIDoesNotEmbedOfficialAutomations(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "github.com/monoes/mono-agent/automations" {
			t.Fatal("monoagentcli depends on github.com/monoes/mono-agent/automations; only tests may import it")
		}
	}
}

// doctor on a fresh install says where automations come from now, without
// failing.
func TestDoctorFreshHomePointsAtLibrary(t *testing.T) {
	withoutTestSeed(t)
	home := t.TempDir()
	out, _ := runDoctor(t, home, "doctor", "--json", "--check", health.CheckAutomationPackages)
	r := resultByID(decodeDoctor(t, out), health.CheckAutomationPackages)
	if r.Status != health.StatusInfo || !strings.Contains(r.Summary, "monoes.me") ||
		!strings.Contains(r.Detail, "monoagentcli library install automation <id>") {
		t.Fatalf("automations.packages on a fresh home = %+v", r)
	}
	if r.Fix == nil || r.Fix.Command != "monoagentcli library login" || !strings.Contains(r.Detail, "monoagentcli library login") {
		t.Fatalf("fix = %+v", r.Fix)
	}
}
