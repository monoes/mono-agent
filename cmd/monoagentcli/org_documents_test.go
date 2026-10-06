package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runOrgDocuments(t *testing.T, root string, args ...string) (map[string]interface{}, error) {
	t.Helper()
	cmd := newOrgCmd(&globalConfig{})
	cmd.SetArgs(append([]string{"--project", root, "documents"}, args...))
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		return nil, runErr
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("org documents printed non-JSON %q: %v", out, err)
	}
	return m, nil
}

// The synthetic run: a rework cycle that was accepted and an exhausted cap.
func TestOrgDocumentsPrintsReworkAndExhaustedCap(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join("..", "..", "internal", "orgbridge", "testdata", "documents-synthetic")
	for from, to := range map[string]string{
		"org-rework.json": "rework.json",
		"events.jsonl":    "rework/docs/run-syn/events.jsonl",
		"notices.jsonl":   "rework/docs/run-syn/notices.jsonl",
	} {
		b, err := os.ReadFile(filepath.Join(src, from))
		if err != nil {
			t.Fatal(err)
		}
		writeOrgFile(t, root, to, string(b))
	}
	m, err := runOrgDocuments(t, root, "rework")
	if err != nil {
		t.Fatal(err)
	}
	if m["run"] != "run-syn" {
		t.Fatalf("run = %v", m["run"])
	}
	sum := m["summary"].(map[string]interface{})
	if sum["documents"] != float64(4) || sum["cap_hit"] != float64(1) {
		t.Fatalf("summary = %v", sum)
	}
	if _, err := runOrgDocuments(t, root, "rework", "--run", "../x"); err == nil {
		t.Fatal("a traversing run id must be refused")
	}
	if _, err := runOrgDocuments(t, root, "bad/name"); err == nil {
		t.Fatal("a bad org name must be refused")
	}
}
