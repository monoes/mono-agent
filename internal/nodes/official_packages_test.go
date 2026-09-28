package nodes

import (
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/automations"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

// The app no longer ships the official automation packages; tests get them
// from the repo's automations/: seeded by BootAutomations into test homes,
// and readable without a registry.
func init() {
	automation.TestSeed = automations.FS()
	action.SetTestFallback(automations.FS())
}

// TestBootAutomations_KeepsPreviouslySeededBuiltins: a home an earlier
// version seeded keeps its built-in packages loadable after a boot that
// seeds nothing (the release behaviour).
func TestBootAutomations_KeepsPreviouslySeededBuiltins(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".monoagent")
	old, err := automation.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Seed(automations.FS()); err != nil {
		t.Fatal(err)
	}
	prev := automation.TestSeed
	automation.TestSeed = nil
	t.Cleanup(func() { automation.TestSeed = prev; action.SetDefSource(nil) })
	bootMu.Lock()
	bootedReg, bootedHome = nil, ""
	bootMu.Unlock()

	reg, err := BootAutomations(home)
	if err != nil {
		t.Fatal(err)
	}
	info, err := reg.Info("gemini")
	if err != nil {
		t.Fatal(err)
	}
	if info.Source != automation.SourceBuiltin || !info.Enabled || !info.Available {
		t.Fatalf("gemini after boot = %+v", info)
	}
	if _, err := action.CurrentDefSource().Load("gemini", "generate_text"); err != nil {
		t.Fatalf("load gemini/generate_text through the booted registry: %v", err)
	}
}
