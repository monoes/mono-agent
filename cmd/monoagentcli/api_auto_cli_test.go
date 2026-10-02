package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

// enableAPIAuto switches the api_auto surface on for a profile in the test database.
func enableAPIAuto(t *testing.T, dbPath, profile string) {
	t.Helper()
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := jevconf.SetEnabled(db.DB, profile, jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}
}

// `api models` says whether the auto model works for the profile and what is
// missing when it does not, and how many models Jev would pick among: those the
// listener's policy allows.
func TestAPIModelsSaysWhetherAutoWorks(t *testing.T) {
	db := newAPITestDB(t)
	fakeAPIMonomind(t)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("TYPESAFE_API_KEY", "")

	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeModels(t, out); m.Auto.Available || m.Auto.Candidates != 0 || !strings.Contains(m.Auto.Missing, "jev enable api_auto") {
		t.Errorf("surface off: %+v", m.Auto)
	}

	enableAPIAuto(t, db, "default")
	out, _, _ = runAPI(t, db, "default", true, "models")
	if m := decodeModels(t, out); m.Auto.Available || !strings.Contains(m.Auto.Missing, "Jev key") {
		t.Errorf("surface on, no key: %+v", m.Auto)
	}

	t.Setenv("TYPESAFE_API_KEY", "test-key")
	out, _, _ = runAPI(t, db, "default", true, "models")
	m := decodeModels(t, out)
	allowed := 0
	for _, x := range m.Models {
		if x.Allowed {
			allowed++
		}
	}
	if !m.Auto.Available || m.Auto.Missing != "" || m.Auto.Candidates != allowed || allowed < 2 {
		t.Errorf("surface on and a key: %+v with %d models allowed", m.Auto, allowed)
	}
	out, _, _ = runAPI(t, db, "default", true, "models", "--for", "network")
	if n := decodeModels(t, out); n.Auto.Candidates != 1 {
		t.Errorf("a network listener serves chat-only: Jev picks among %d, want 1 (claude)", n.Auto.Candidates)
	}

	text, _, err := runAPI(t, db, "default", false, "models")
	if err != nil || !strings.Contains(text, "auto: available") {
		t.Errorf("the table must say it too: %q, %v", text, err)
	}
}

// With no runtime installed there is nothing for Jev to pick among, and the
// gateway does not serve auto: the CLI says so instead of promising it.
func TestAPIModelsSaysAutoNeedsAModel(t *testing.T) {
	db := newAPITestDB(t)
	if runtime.GOOS == "windows" {
		t.Skip("the fake monomind is a shell script")
	}
	script := `#!/bin/sh
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.22.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","agent-models","agent-exec-sandbox"]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "scan" ]; then
  echo '{"v":1,"agents":[]}'
  exit 0
fi
exit 2
`
	bin := filepath.Join(t.TempDir(), "monomind")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	enableAPIAuto(t, db, "default")

	out, _, err := runAPI(t, db, "default", true, "models")
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeModels(t, out); m.Auto.Available || m.Auto.Candidates != 0 || !strings.Contains(m.Auto.Missing, "model") {
		t.Errorf("nothing installed: %+v", m.Auto)
	}
}

func TestAPIStatusSaysWhetherAutoWorks(t *testing.T) {
	db := newAPITestDB(t)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "none.json"))
	t.Setenv("MONOAGENT_API_CONFINEMENT", "")
	t.Setenv("MONOAGENT_HTTPAPI_ADDR", "127.0.0.1:1")
	t.Setenv("MONOAGENT_API_V1_ADDR", "")
	t.Setenv("TYPESAFE_API_KEY", "")

	out, _, err := runAPI(t, db, "default", true, "status")
	if err != nil {
		t.Fatal(err)
	}
	if st := decodeStatus(t, out); st.Auto.Available || !strings.Contains(st.Auto.Missing, "jev enable api_auto") {
		t.Errorf("surface off: %+v", st.Auto)
	}
	text, _, _ := runAPI(t, db, "default", false, "status")
	if !strings.Contains(text, "Auto model: off") || !strings.Contains(text, "jev enable api_auto") {
		t.Errorf("the text must say what to set up:\n%s", text)
	}

	enableAPIAuto(t, db, "default")
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	out, _, _ = runAPI(t, db, "default", true, "status")
	if st := decodeStatus(t, out); !st.Auto.Available {
		t.Errorf("surface on and a key: %+v", st.Auto)
	}
	if text, _, _ := runAPI(t, db, "default", false, "status"); !strings.Contains(text, "Auto model: available") {
		t.Errorf("the text must say it works:\n%s", text)
	}
}
