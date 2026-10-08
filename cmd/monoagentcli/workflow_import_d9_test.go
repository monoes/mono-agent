package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// editWorkflowLocally changes the "Set" node's config the way the desktop
// editor saves a workflow.
func editWorkflowLocally(t *testing.T, cfg *globalConfig, id string) {
	t.Helper()
	db, err := initDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := openWorkflowStore(db)
	wf, err := store.GetWorkflow(context.Background(), id)
	if err != nil || wf == nil {
		t.Fatalf("get %s: %v", id, err)
	}
	for i := range wf.Nodes {
		if wf.Nodes[i].Name == "Set" {
			wf.Nodes[i].Config = map[string]any{"assignments": []any{map[string]any{"field": "edited", "value": "by-user"}}}
		}
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatal(err)
	}
}

const d9FileWithID = `"id": "wf-d9", "name": "test-wf",`

// TestWorkflowImportAfterEditMakesOneCopy (D9-a/b): after the original is
// edited, three re-imports of the same file make one copy; the later ones
// are "unchanged" and point at it. The copy warning says the match was by id.
func TestWorkflowImportAfterEditMakesOneCopy(t *testing.T) {
	_, cfg := persistTestEnv(t)
	path := writeTempWorkflow(t, strings.Replace(validWorkflowFile, `"name": "test-wf",`, d9FileWithID, 1))
	first := importJSON(t, cfg, "--file", path)
	if first.ID != "wf-d9" {
		t.Fatalf("first import = %+v", first)
	}
	editWorkflowLocally(t, cfg, first.ID)

	cp := importJSON(t, cfg, "--file", path)
	if cp.Status != importCreated || cp.ID == first.ID || len(cp.Warnings) != 1 ||
		!strings.Contains(cp.Warnings[0], "a workflow with this id already exists and was edited locally: wf-d9") ||
		!strings.Contains(cp.Warnings[0], "--replace wf-d9") || cp.CopyOf != "wf-d9" || cp.CopyReason != "id" {
		t.Fatalf("re-import after the edit = %+v", cp)
	}
	for i := 0; i < 2; i++ {
		again := importJSON(t, cfg, "--file", path)
		if again.Status != importUnchanged || again.ID != cp.ID || again.CopyOf != "" {
			t.Fatalf("re-import %d = %+v, want unchanged %s", i+2, again, cp.ID)
		}
	}
	if n := countWorkflows(t, cfg); n != 2 {
		t.Fatalf("workflows = %d, want the original and one copy", n)
	}
}

// TestWorkflowImportCopyWarningByImport (D9-b): a file without an id whose
// earlier import was edited gets the "imported earlier" wording.
func TestWorkflowImportCopyWarningByImport(t *testing.T) {
	_, cfg := persistTestEnv(t)
	path := writeTempWorkflow(t, validWorkflowFile) // no id
	first := importJSON(t, cfg, "--file", path)
	editWorkflowLocally(t, cfg, first.ID)
	changed := strings.Replace(validWorkflowFile, `"x": 1`, `"x": 5`, 1)
	path2 := path
	if err := writeFileAt(path2, changed); err != nil {
		t.Fatal(err)
	}
	cp := importJSON(t, cfg, "--file", path2)
	if len(cp.Warnings) != 1 || !strings.Contains(cp.Warnings[0], "the workflow imported earlier from this file was edited locally: "+first.ID) ||
		cp.CopyOf != first.ID || cp.CopyReason != "import" {
		t.Fatalf("copy = %+v", cp)
	}
}

// TestWorkflowImportOverwriteRefusesEditedID (D9-c): --overwrite on an id
// that is a locally edited workflow is refused, naming --replace, and
// nothing is created.
func TestWorkflowImportOverwriteRefusesEditedID(t *testing.T) {
	_, cfg := persistTestEnv(t)
	path := writeTempWorkflow(t, strings.Replace(validWorkflowFile, `"name": "test-wf",`, d9FileWithID, 1))
	importJSON(t, cfg, "--file", path, "--overwrite")
	editWorkflowLocally(t, cfg, "wf-d9")

	cmd := newWorkflowImportCmd(cfg)
	cmd.SetArgs([]string{"--file", path, "--overwrite"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var err error
	out := captureStdout(t, func() { err = cmd.Execute() })
	if err == nil || !strings.Contains(err.Error(), "--replace wf-d9") || exitCodeFor(err) != 3 {
		t.Fatalf("--overwrite over an edited id: %v", err)
	}
	// With --json the refusal is also one {"error","code"} document on stdout.
	dec := json.NewDecoder(strings.NewReader(out))
	var body map[string]string
	if derr := dec.Decode(&body); derr != nil || !strings.Contains(body["error"], "--replace wf-d9") || body["code"] != "invalid_input" {
		t.Fatalf("stdout %q: %v", out, derr)
	}
	if dec.More() {
		t.Fatalf("more than one JSON document: %q", out)
	}
	if n := countWorkflows(t, cfg); n != 1 {
		t.Fatalf("workflows = %d, want 1 (no silent copy)", n)
	}
	// --replace is the explicit way.
	if rep := importJSON(t, cfg, "--file", path, "--replace", "wf-d9"); rep.Status != importUpdated || rep.ID != "wf-d9" {
		t.Fatalf("--replace = %+v", rep)
	}
}

func writeFileAt(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

// TestWorkflowImportCopyOfByName: a copy made beside a same-named
// workflow carries copyOf/copyReason "name" next to its warning.
func TestWorkflowImportCopyOfByName(t *testing.T) {
	_, cfg := persistTestEnv(t)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "create", "test-wf")), &created); err != nil {
		t.Fatal(err)
	}
	r := importJSON(t, cfg, "--file", writeTempWorkflow(t, validWorkflowFile))
	if r.Status != importCreated || r.CopyOf != created.ID || r.CopyReason != "name" || len(r.Warnings) != 1 {
		t.Fatalf("import beside a same-named workflow = %+v", r)
	}
	// A plain first import makes no copy.
	_, cfg2 := persistTestEnv(t)
	if r := importJSON(t, cfg2, "--file", writeTempWorkflow(t, validWorkflowFile)); r.CopyOf != "" || r.CopyReason != "" {
		t.Fatalf("fresh import = %+v", r)
	}
}

// TestWorkflowImportJSONErrorNotFound: a missing --file is
// {"error","code":"not_found"} on stdout with --json (exit 2).
func TestWorkflowImportJSONErrorNotFound(t *testing.T) {
	_, cfg := persistTestEnv(t)
	cmd := newWorkflowImportCmd(cfg)
	cmd.SetArgs([]string{"--file", "/nonexistent/wf.json"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var err error
	out := captureStdout(t, func() { err = cmd.Execute() })
	if err == nil || exitCodeFor(err) != 2 {
		t.Fatalf("err = %v", err)
	}
	var body map[string]string
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &body) != nil || body["code"] != "not_found" || body["error"] == "" {
		t.Fatalf("stdout %q", out)
	}
	// Without --json nothing goes to stdout.
	cfg.JSONOutput = false
	cmd = newWorkflowImportCmd(cfg)
	cmd.SetArgs([]string{"--file", "/nonexistent/wf.json"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if out := captureStdout(t, func() { _ = cmd.Execute() }); strings.TrimSpace(out) != "" {
		t.Fatalf("human mode stdout %q", out)
	}
}
