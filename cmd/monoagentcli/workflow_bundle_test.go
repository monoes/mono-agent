package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// bundleTestWorkflow is a workflow using the acme-test automation.
const bundleTestWorkflow = `{
  "name": "bundle-wf",
  "nodes": [
    {"id": "t", "type": "trigger.manual", "name": "Trigger", "position": {"x": 0, "y": 0}, "config": {}},
    {"id": "a", "type": "acme-test.get_title", "name": "Title", "position": {"x": 200, "y": 0}, "config": {}}
  ],
  "connections": [{"id": "t-a", "source": "t", "target": "a"}]
}`

func TestWorkflowAutomationIDs(t *testing.T) {
	got := workflowAutomationIDs([]workflow.WorkflowFileNode{
		{Type: "b.x"}, {Type: "a.y"}, {Type: "b.z"}, {Type: "nodot"}, {Type: ".x"},
	})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("workflowAutomationIDs = %v", got)
	}
}

// TestWorkflowBundleRoundTrip: export with --bundle-automations on one
// machine, import into a fresh HOME: --json without --yes reports the
// package as missing, --yes installs it.
func TestWorkflowBundleRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	installTestAutomation(t)
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "src.db"), JSONOutput: true, ProfileID: "default"}

	src := filepath.Join(t.TempDir(), "wf.json")
	if err := os.WriteFile(src, []byte(bundleTestWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
	var imported struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "import", "--file", src)), &imported); err != nil {
		t.Fatal(err)
	}
	bundled := filepath.Join(t.TempDir(), "bundled.json")
	runWorkflowSubcmd(t, cfg, "export", imported.ID, "--bundle-automations", "-o", bundled)

	raw, err := os.ReadFile(bundled)
	if err != nil {
		t.Fatal(err)
	}
	var doc workflowBundleFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Automations) != 1 || doc.Automations["acme-test"].Mpkg == "" || doc.Automations["acme-test"].Version != "1.0.0" {
		t.Fatalf("bundle should hold exactly acme-test (not trigger): %v", keysOf(doc.Automations))
	}
	// Backward compatible: the plain workflow parser still reads it.
	if wf, err := parseWorkflowDefinition(raw); err != nil || len(wf.Nodes) != 2 {
		t.Fatalf("plain parse of a bundled file: %v (%d nodes)", err, len(wf.Nodes))
	}
	// Without the flag nothing is bundled.
	plain := filepath.Join(t.TempDir(), "plain.json")
	runWorkflowSubcmd(t, cfg, "export", imported.ID, "-o", plain)
	var plainDoc workflowBundleFile
	if b, _ := os.ReadFile(plain); json.Unmarshal(b, &plainDoc) != nil || plainDoc.Automations != nil {
		t.Fatal("export without --bundle-automations must not add an automations field")
	}

	// A fresh machine.
	t.Setenv("HOME", t.TempDir())
	cfg2 := &globalConfig{DBPath: filepath.Join(t.TempDir(), "dst.db"), JSONOutput: true, ProfileID: "default"}
	report := func(out string) map[string]string {
		t.Helper()
		var res struct {
			Automations []bundleImportItem `json:"automations"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("parse import output %q: %v", out, err)
		}
		m := map[string]string{}
		for _, it := range res.Automations {
			m[it.ID] = it.Status + it.Error
		}
		return m
	}
	if got := report(runWorkflowSubcmd(t, cfg2, "import", "--file", bundled)); got["acme-test"] != "missing" {
		t.Fatalf("--json without --yes should report missing, got %v", got)
	}
	reg, err := openAutomationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Info("acme-test"); err == nil {
		t.Fatal("nothing may be installed without --yes")
	}
	if got := report(runWorkflowSubcmd(t, cfg2, "import", "--file", bundled, "--yes")); got["acme-test"] != "installed" {
		t.Fatalf("--yes should install, got %v", got)
	}
	info, err := reg.Info("acme-test")
	if err != nil || info.Version != "1.0.0" {
		t.Fatalf("acme-test after bundle import: %+v, %v", info, err)
	}
	if got := report(runWorkflowSubcmd(t, cfg2, "import", "--file", bundled, "--yes")); got["acme-test"] != "present" {
		t.Fatalf("an installed package should be reported present, got %v", got)
	}
}

func TestWorkflowBundleRejectsTamperedPackage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	raw, _ := json.Marshal(map[string]any{
		"name": "x", "nodes": []any{}, "connections": []any{},
		"automations": map[string]any{"acme-test": map[string]string{
			"version": "1.0.0", "sha256": "00", "mpkg": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA==",
		}},
	})
	items := handleBundledAutomations(raw, bundleImportOptions{yes: true})
	if len(items) != 1 || items[0].Status != "failed" {
		t.Fatalf("tampered bundle: %+v", items)
	}
}

func keysOf(m map[string]bundledAutomation) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
