package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// TestCLI_BootsInstalledAutomations: a user package installed into the
// registry is visible to CLI paths that read actions without building a
// node registry first (node schema) and to workflow validation.
func TestCLI_BootsInstalledAutomations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	skipAutomationBoot = false
	t.Cleanup(func() { skipAutomationBoot = true; action.SetDefSource(nil) })

	reg, err := automation.Open(filepath.Join(home, ".monoagent"))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "acme-crm")
	writeTestFile(t, filepath.Join(src, "automation.json"), `{
	  "schema": "monoagent.automation/v1",
	  "id": "acme-crm", "name": "Acme CRM", "version": "1.0.0",
	  "site": {"startUrl": "https://app.acme-crm.com/", "domains": ["app.acme-crm.com"]},
	  "permissions": {"steps": ["navigate"], "scripts": [], "downloads": false},
	  "actions": ["list_deals"],
	  "policy": {"tier": "standard"}
	}`)
	writeTestFile(t, filepath.Join(src, "actions", "list_deals.json"), `{
	  "actionType": "list_deals", "automation": "acme-crm", "sideEffects": "read",
	  "inputs": {"optional": [{"name": "stage", "type": "string", "enum": ["open", "won"]}]},
	  "steps": [{"id": "open", "type": "navigate", "url": "https://app.acme-crm.com/deals"}]
	}`)
	if res, err := reg.Install(src, automation.InstallOptions{Source: automation.SourceLocal}); err != nil || !res.Installed {
		t.Fatalf("Install: %v %+v", err, res)
	}
	action.SetDefSource(nil) // the CLI must boot it itself

	out, err := runRoot(t, "--db-path", filepath.Join(home, "t.db"), "node", "schema", "acme-crm.list_deals")
	if err != nil {
		t.Fatalf("node schema: %v\n%s", err, out)
	}
	var schema struct {
		Fields []struct{ Key, Type string } `json:"fields"`
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &schema); err != nil {
		t.Fatalf("node schema output: %v\n%s", err, out)
	}
	var stage bool
	for _, f := range schema.Fields {
		stage = stage || (f.Key == "stage" && f.Type == "select")
	}
	if !stage {
		t.Errorf("generated form lacks the stage select:\n%s", out)
	}
	if action.CurrentDefSource() == nil {
		t.Error("root command did not boot the automation registry")
	}

	wf := filepath.Join(t.TempDir(), "wf.json")
	writeTestFile(t, wf, `{
	  "name": "deals",
	  "nodes": [
	    {"id": "t", "type": "trigger.manual", "name": "Start", "config": {}},
	    {"id": "d", "type": "acme-crm.list_deals", "name": "Deals", "config": {"stage": "open"}}
	  ],
	  "connections": [{"source_node_id": "t", "target_node_id": "d"}]
	}`)
	out, err = runRoot(t, "--db-path", filepath.Join(home, "t.db"), "workflow", "validate", "--file", wf)
	if err != nil || !strings.Contains(out, "valid") {
		t.Fatalf("workflow validate: %v\n%s", err, out)
	}
}

func TestNeedsAutomations(t *testing.T) {
	root := newRootCmd()
	root.InitDefaultCompletionCmd()
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"node", "schema"}, true},
		{[]string{"workflow", "validate"}, true},
		{[]string{"mcp"}, true},
		{[]string{"httpapi"}, true},
		{[]string{"daemon"}, true},
		{[]string{"completion", "bash"}, false},
	} {
		cmd, _, err := root.Find(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got := needsAutomations(cmd); got != c.want {
			t.Errorf("needsAutomations(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}
