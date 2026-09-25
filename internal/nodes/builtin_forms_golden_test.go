package nodes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/workflow"
)

// TestBuiltinForms_MatchMaster: every built-in browser node type resolves to
// exactly the form it had before automation packages (captured from
// origin/master's LoadDefaultSchema into testdata), both with the legacy
// loader and with the automation registry booted.
func TestBuiltinForms_MatchMaster(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "builtin_forms_master.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]json.RawMessage
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}

	check := func(label string) {
		t.Helper()
		for _, nt := range browserNodeTypes() {
			want, ok := golden[nt]
			if !ok {
				t.Errorf("%s: no golden form for %s", label, nt)
				continue
			}
			s, err := workflow.LoadDefaultSchema(nt)
			if err != nil {
				t.Errorf("%s: %s: %v", label, nt, err)
				continue
			}
			got, _ := json.Marshal(s)
			if !jsonEqual(t, got, want) {
				t.Errorf("%s: %s form changed\n got: %s\nwant: %s", label, nt, got, want)
			}
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	action.SetDefSource(nil)
	check("legacy")

	if _, err := BootAutomations(filepath.Join(home, ".monoagent")); err != nil {
		t.Fatalf("BootAutomations: %v", err)
	}
	t.Cleanup(func() { action.SetDefSource(nil) })
	check("registry")
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}
