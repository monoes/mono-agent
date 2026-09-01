package orgdesign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// realOrgExample is a byte-for-byte copy of a real monomind org config
// (packages/@monomind/cli's own .monomind/orgs/monoagent-dev.json), used to
// prove round-trip fidelity: every field this package doesn't model
// (policy, run_config internals, max_turns_per_message, ...) must survive a
// Load->Save cycle unchanged.
const realOrgExample = `{
  "name": "monoagent-dev",
  "goal": "Build missing mono-agent nodes and fix bugs.",
  "status": "stopped",
  "schedule": null,
  "run_config": {
    "max_concurrent_agents": 4,
    "budget_tokens": 10000000,
    "memory_namespace": "org:monoagent-dev",
    "max_turns_per_message": 100,
    "idle_minutes": 10,
    "workspace": "/Volumes/media/projects/monoes/mono-agent",
    "circuit_breaker": {
      "failure_threshold": 3,
      "cooldown_ms": 30000
    }
  },
  "roles": [
    {
      "id": "dev-lead",
      "title": "Dev Lead",
      "type": "boss",
      "reports_to": null,
      "responsibilities": ["Lead the org."]
    },
    {
      "id": "node-developer",
      "title": "Node Developer",
      "type": "specialist",
      "reports_to": "dev-lead",
      "max_turns_per_message": 100,
      "responsibilities": ["Implement nodes."],
      "policy": {
        "git": "commit",
        "fileWrite": ["internal/nodes/**", "internal/noderegistry/**"]
      }
    },
    {
      "id": "node-tester",
      "title": "Node Tester",
      "type": "specialist",
      "reports_to": "dev-lead",
      "responsibilities": ["Test nodes."],
      "policy": {
        "git": "read",
        "fileWrite": []
      }
    }
  ]
}`

func TestRoundTripFidelity(t *testing.T) {
	var d Doc
	if err := json.Unmarshal([]byte(realOrgExample), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Unknown top-level and role-level fields must survive.
	if len(d.RunConfig) == 0 {
		t.Fatalf("run_config was not captured")
	}
	if _, ok := d.RunConfig["circuit_breaker"]; !ok {
		t.Errorf("run_config.circuit_breaker missing after unmarshal")
	}

	dev, idx := d.FindRole("node-developer")
	if idx == -1 {
		t.Fatalf("node-developer role not found")
	}
	if _, ok := dev.Extra["policy"]; !ok {
		t.Errorf("role.policy missing from Extra after unmarshal")
	}
	if _, ok := dev.Extra["max_turns_per_message"]; !ok {
		t.Errorf("role.max_turns_per_message missing from Extra after unmarshal")
	}

	remarshaled, err := json.Marshal(&d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var original, roundTripped map[string]interface{}
	if err := json.Unmarshal([]byte(realOrgExample), &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(remarshaled, &roundTripped); err != nil {
		t.Fatal(err)
	}

	assertDeepJSONEqual(t, "run_config.circuit_breaker.failure_threshold",
		dig(original, "run_config", "circuit_breaker", "failure_threshold"),
		dig(roundTripped, "run_config", "circuit_breaker", "failure_threshold"))

	roles, ok := roundTripped["roles"].([]interface{})
	if !ok || len(roles) != 3 {
		t.Fatalf("expected 3 roles after round-trip, got %v", roundTripped["roles"])
	}
	role1, ok := roles[1].(map[string]interface{})
	if !ok {
		t.Fatalf("roles[1] not an object")
	}
	if role1["id"] != "node-developer" {
		t.Fatalf("role order not preserved: roles[1].id = %v", role1["id"])
	}
	policy, ok := role1["policy"].(map[string]interface{})
	if !ok {
		t.Fatalf("roles[1].policy missing after round-trip")
	}
	if policy["git"] != "commit" {
		t.Errorf("roles[1].policy.git = %v, want commit", policy["git"])
	}
	if role1["max_turns_per_message"] != float64(100) {
		t.Errorf("roles[1].max_turns_per_message = %v, want 100", role1["max_turns_per_message"])
	}
}

func dig(m map[string]interface{}, path ...string) interface{} {
	var cur interface{} = m
	for _, p := range path {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func assertDeepJSONEqual(t *testing.T, label string, a, b interface{}) {
	t.Helper()
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if string(ab) != string(bb) {
		t.Errorf("%s: %s != %s", label, ab, bb)
	}
}

func TestNullReportsToPreserved(t *testing.T) {
	d := &Doc{
		Name: "x", Goal: "g", Roles: []Role{{ID: "root", Title: "Root", Type: "boss", ReportsTo: nil, Responsibilities: []string{}}},
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	json.Unmarshal(b, &raw)
	roles := raw["roles"].([]interface{})
	r0 := roles[0].(map[string]interface{})
	if _, present := r0["reports_to"]; !present {
		t.Fatalf("reports_to key must be present (as null), got: %s", b)
	}
	if r0["reports_to"] != nil {
		t.Errorf("reports_to = %v, want null", r0["reports_to"])
	}
}

func TestIsOrgConfigFile(t *testing.T) {
	cases := map[string]bool{
		"monoagent-dev.json":          true,
		"docs-team.json":              true,
		"foo-state.json":              false,
		"foo-goals.json":              false,
		"foo-threads.json":            false,
		"foo-activity.json":           false,
		"foo-approvals.json":          false,
		"foo-members.json":            false,
		"foo-secrets.json":            false,
		"foo-budgets.json":            false,
		"foo-routines.json":           false,
		"foo-issues.json":             false,
		"foo-projects.json":           false,
		"foo-workspaces.json":         false,
		"foo-worktrees.json":          false,
		"foo-environments.json":       false,
		"foo-plugins.json":            false,
		"foo-adapters.json":           false,
		"foo-join-requests.json":      false,
		"foo-bootstrap.json":          false,
		"foo-project-workspaces.json": false,
		"foo-approval-comments.json":  false,
		"foo-skills.json":             false,
		"legacy.v1.json":              false,
		"._hidden.json":               false,
		"state-machine.json":          true, // substring "state" but not the "-state" SUFFIX
		"issues-triage.json":          true, // substring "issues" but not the "-issues" SUFFIX
		"x.json.tmp":                  false,
		"notjson.txt":                 false,
	}
	for name, want := range cases {
		if got := IsOrgConfigFile(name); got != want {
			t.Errorf("IsOrgConfigFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	d := NewOrg("test-org", "test goal", NewOrgOptions{})
	if _, err := d.AddRole(Role{Title: "Worker", ReportsTo: strPtr("lead")}); err != nil {
		t.Fatal(err)
	}
	sha1, err := Save(dir, d)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if sha1 == "" {
		t.Fatal("Save returned empty sha")
	}

	path, _ := ConfigPath(dir, "test-org")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf(".tmp file left behind")
	}

	loaded, err := Load(dir, "test-org")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(loaded.Roles))
	}

	names, err := ListOrgNames(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "test-org" {
		t.Fatalf("ListOrgNames = %v", names)
	}
}

func TestSaveRefusesInvalidDoc(t *testing.T) {
	dir := t.TempDir()
	d := &Doc{Name: "bad", Goal: "g"} // no roles
	if _, err := Save(dir, d); err == nil {
		t.Fatal("expected Save to refuse a roleless org")
	}
	if _, err := os.Stat(filepath.Join(dir, orgDir, "bad.json")); !os.IsNotExist(err) {
		t.Fatal("Save must not write a file when validation fails")
	}
}
