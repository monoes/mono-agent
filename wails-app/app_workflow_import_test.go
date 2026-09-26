package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWorkflowImportArgs(t *testing.T) {
	got := workflowImportArgs("p1", "/w/flow.json", WorkflowImportOptions{AsNew: true, Yes: true})
	want := []string{"--profile", "p1", "--json", "workflow", "import", "--file", "/w/flow.json", "--as-new", "--yes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	// Pasted JSON goes on stdin: no --file.
	got = workflowImportArgs("", "", WorkflowImportOptions{})
	if !reflect.DeepEqual(got, []string{"--json", "workflow", "import"}) {
		t.Fatalf("stdin: %v", got)
	}
}

func TestBundleReviewLinesMerged(t *testing.T) {
	stderr := []byte("note: something\nBundled automation acme 1.0.0 — publisher Jane — domains app.acme.com — steps: click\nother\n")
	lines := bundleReviewLines(stderr)
	if len(lines) != 1 || lines[0][:25] != "Bundled automation acme 1" {
		t.Fatalf("lines = %q", lines)
	}
	out := withReviewLines(`{"id":"w1","status":"created"}`, lines)
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil || obj["id"] != "w1" || len(obj["reviewLines"].([]any)) != 1 {
		t.Fatalf("merged = %s (%v)", out, err)
	}
	if withReviewLines("not json", lines) != "not json" {
		t.Fatal("non-JSON output changed")
	}
	if withReviewLines(`{"id":"w1"}`, nil) != `{"id":"w1"}` {
		t.Fatal("output changed without review lines")
	}
}
