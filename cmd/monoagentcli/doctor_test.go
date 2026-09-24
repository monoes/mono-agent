package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/health"
)

// runDoctor runs `monoagentcli <args>` against a throwaway HOME.
func runDoctor(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append(args, "--db-path", filepath.Join(home, ".monoagent", "monoagent.db")))
	err := root.Execute()
	return out.String(), err
}

func decodeDoctor(t *testing.T, out string) doctorJSON {
	t.Helper()
	var rep struct {
		health.Report
		Fixes []fixOutcome `json:"fixes"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor --json output is not JSON: %v\n%s", err, out)
	}
	return doctorJSON{Report: &rep.Report, Fixes: rep.Fixes}
}

func resultByID(rep doctorJSON, id string) health.Result {
	for _, r := range rep.Results {
		if r.ID == id {
			return r
		}
	}
	return health.Result{}
}

func TestDoctorFreshHomeFailsThenFixes(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()

	out, err := runDoctor(t, home, "doctor", "--json", "--group", "core")
	if exitCodeFor(err) != 1 {
		t.Fatalf("fresh home: exit %d (%v), want 1", exitCodeFor(err), err)
	}
	rep := decodeDoctor(t, out)
	if rep.V != health.SchemaVersion {
		t.Fatalf("schema v%d", rep.V)
	}
	db := resultByID(rep, health.CheckDB)
	if db.Status != health.StatusFail || db.Fix == nil || db.Fix.ID != health.FixDBMigrate {
		t.Fatalf("core.db on fresh home: %+v", db)
	}
	if st := resultByID(rep, health.CheckProfile).Status; st != health.StatusSkip {
		t.Fatalf("core.profile should wait on the database, got %q", st)
	}

	out, err = runDoctor(t, home, "doctor", "--json", "--group", "core", "--fix")
	if err != nil {
		t.Fatalf("doctor --fix: %v\n%s", err, out)
	}
	rep = decodeDoctor(t, out)
	for _, id := range []string{health.CheckHome, health.CheckDB, health.CheckProfile} {
		if st := resultByID(rep, id).Status; st != health.StatusOK {
			t.Errorf("%s after --fix: %q", id, st)
		}
	}
	applied := map[string]string{}
	for _, f := range rep.Fixes {
		applied[f.ID] = f.Outcome
	}
	if applied[health.FixDBMigrate] != "applied" || applied[health.FixProfileLayout] != "applied" {
		t.Errorf("fix outcomes: %v", rep.Fixes)
	}
}

func TestDoctorFixStreamsNDJSON(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()

	out, err := runDoctor(t, home, "doctor", "fix", health.FixDBMigrate, "--json")
	if err != nil {
		t.Fatalf("doctor fix: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines {
		var ev fixEvent
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("non-NDJSON line %q", l)
		}
	}
	var last fixEvent
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &last)
	if last.Kind != "done" {
		t.Fatalf("last event %+v, want done", last)
	}

	if _, err := runDoctor(t, home, "doctor", "fix", "no.such.fix"); exitCodeFor(err) != 2 {
		t.Fatalf("unknown fix: exit %d, want 2", exitCodeFor(err))
	}
}

func TestApplyReportFixesSkipsOptional(t *testing.T) {
	applied := map[string]bool{}
	reg := health.NewRegistry(nil, []health.Fix{
		{FixInfo: health.FixInfo{ID: "a", Safety: health.SafetyAuto},
			Apply: func(context.Context, *health.Env, func(string)) error { applied["a"] = true; return nil }},
		{FixInfo: health.FixInfo{ID: "rt", Safety: health.SafetyConfirm, Optional: true},
			ApplyArg: func(_ context.Context, _ *health.Env, arg string, _ func(string)) error {
				applied[arg] = true
				return nil
			}},
	})
	rep := &health.Report{Results: []health.Result{
		{ID: "x", Status: health.StatusFail, Fix: &health.FixInfo{ID: "a"}},
		{ID: "y", Status: health.StatusInfo, Fix: &health.FixInfo{ID: "rt:claude", Optional: true}},
	}}
	t.Setenv("HOME", t.TempDir())
	outcomes := applyReportFixes(context.Background(), &globalConfig{DBPath: "~/.monoagent/x.db"}, reg, rep,
		map[string]bool{}, func(health.FixInfo) bool { return true }, func(string) {})
	if !applied["a"] || applied["claude"] {
		t.Fatalf("applied %v; optional runtime install must not run under --fix", applied)
	}
	if len(outcomes) != 1 || outcomes[0].ID != "a" {
		t.Fatalf("outcomes %v", outcomes)
	}
}

func TestClaudeSkillsStateAndMCPRegistration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if found, _, _ := claudeSkillsState(); found {
		t.Fatal("no ~/.claude: want not found")
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, missing, _ := claudeSkillsState(); len(missing) != len(claudeSkillNames) {
		t.Fatalf("missing = %v", missing)
	}
	if err := installClaudeSkill(false); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, ".claude", "skills", claudeSkillNames[0]), []byte("old"), 0o644)
	if _, missing, stale := claudeSkillsState(); len(missing) != 0 || len(stale) != 1 {
		t.Fatalf("missing %v stale %v", missing, stale)
	}

	if _, reg, _ := claudeMCPRegistration(); reg {
		t.Fatal("no ~/.claude.json: not registered")
	}
	os.WriteFile(filepath.Join(home, ".claude.json"),
		[]byte(`{"mcpServers":{"ma":{"command":"/usr/local/bin/monoagentcli","args":["mcp"]}}}`), 0o644)
	if found, reg, _ := claudeMCPRegistration(); !found || !reg {
		t.Fatalf("registered entry not detected: %v %v", found, reg)
	}
}
