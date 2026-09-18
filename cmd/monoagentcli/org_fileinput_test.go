package main

import (
	"context"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// seedFileInputWorkflow adds a workflow whose write node takes its path from
// the trigger input (C-46).
func seedFileInputWorkflow(t *testing.T, f *orgCLIFixture) string {
	t.Helper()
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := workflow.NewSQLiteWorkflowStore(db.DB)
	ctx := context.Background()
	wf := &workflow.Workflow{Name: "Save note", ProfileID: "default"}
	if err := store.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWorkflowNodes(ctx, wf.ID, []workflow.WorkflowNode{
		{ID: wf.ID + "-t", WorkflowID: wf.ID, Type: "trigger.manual", Name: "start"},
		{ID: wf.ID + "-w", WorkflowID: wf.ID, Type: "data.write_binary_file", Name: "save",
			Config: map[string]interface{}{"file_path": "{{ $json.input.path }}", "field": "text", "encoding": "utf8"}},
	}); err != nil {
		t.Fatal(err)
	}
	return wf.ID
}

func fileInputsOf(t *testing.T, v interface{}) []map[string]interface{} {
	t.Helper()
	list, _ := v.([]interface{})
	out := make([]map[string]interface{}, 0, len(list))
	for _, it := range list {
		out = append(out, it.(map[string]interface{}))
	}
	return out
}

func TestOrgCLIFlagsFileInputWorkflows(t *testing.T) {
	f := newOrgCLIFixture(t)
	wfID := seedFileInputWorkflow(t, f)

	add := f.mustRun(t, "automation", "add", "growth", "--workflow", wfID, "--alias", "save_note")
	nodes := fileInputsOf(t, add["automation"].(map[string]interface{})["file_input_nodes"])
	if len(nodes) != 1 || nodes[0]["node"] != "save (data.write_binary_file)" || nodes[0]["access"] != "write" || nodes[0]["confined"] != true {
		t.Fatalf("automation file_input_nodes = %v", nodes)
	}
	list := f.mustRun(t, "automation", "list", "growth")
	if n := fileInputsOf(t, list["automations"].([]interface{})[0].(map[string]interface{})["file_input_nodes"]); len(n) != 1 {
		t.Fatalf("automation list file_input_nodes = %v", n)
	}

	g := f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "save_note")
	if w := strings.Join(toStrings(g["warnings"]), "\n"); !strings.Contains(w, "paths taken from its input") {
		t.Fatalf("grant add warnings = %v", g["warnings"])
	}
	if n := fileInputsOf(t, g["grant"].(map[string]interface{})["file_input_nodes"]); len(n) != 1 {
		t.Fatalf("grant view file_input_nodes = %v", n)
	}
	gl := f.mustRun(t, "grant", "list", "growth")
	if n := fileInputsOf(t, gl["grants"].([]interface{})[0].(map[string]interface{})["file_input_nodes"]); len(n) != 1 {
		t.Fatalf("grant list file_input_nodes = %v", n)
	}

	eff := f.mustRun(t, "effective-tools", "growth", "--role", "writer")
	found := false
	for _, it := range eff["tools"].([]interface{}) {
		m := it.(map[string]interface{})
		if m["name"] == "monoagent__automation_save_note" {
			found = true
			if n := fileInputsOf(t, m["file_input_nodes"]); len(n) != 1 {
				t.Fatalf("effective tool file_input_nodes = %v", m)
			}
		} else if _, ok := m["file_input_nodes"]; ok {
			t.Fatalf("%v carries file_input_nodes", m["name"])
		}
	}
	if !found {
		t.Fatal("granted tool missing from effective-tools")
	}
}

func TestOrgCLINoFileInputWarningForPlainWorkflows(t *testing.T) {
	f := newOrgCLIFixture(t)
	add := f.mustRun(t, "automation", "add", "growth", "--workflow", f.plainWF, "--alias", "summarize")
	if n := fileInputsOf(t, add["automation"].(map[string]interface{})["file_input_nodes"]); len(n) != 0 {
		t.Fatalf("plain workflow flagged: %v", n)
	}
	g := f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "summarize")
	if w := strings.Join(toStrings(g["warnings"]), "\n"); strings.Contains(w, "paths taken from its input") {
		t.Fatalf("plain workflow warned: %v", w)
	}
}
