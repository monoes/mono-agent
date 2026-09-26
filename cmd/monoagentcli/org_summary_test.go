package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func runOrgSummary(t *testing.T, root string, args ...string) map[string]interface{} {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "o.db"), ProfileID: "default"}
	cmd := newOrgCmd(cfg)
	cmd.SetArgs(append([]string{"--project", root, "summary"}, args...))
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("org summary printed non-JSON %q: %v", out, err)
	}
	return m
}

// --fast answers from local files only: two org configs, a queue for one.
func TestOrgSummaryFast(t *testing.T) {
	root := t.TempDir()
	writeOrgFile(t, root, "acme.json", `{"name":"acme"}`)
	writeOrgFile(t, root, "ops.json", `{"name":"ops"}`)
	msg := `{"fromQualified":"hq:ceo","toRole":"lead","subject":"s","body":"b","ts":1789725600000,"messageId":"%s"}` + "\n"
	writeOrgFile(t, root, "acme/inbox.jsonl", strings.ReplaceAll(msg, "%s", "m1")+strings.ReplaceAll(msg, "%s", "m2"))

	m := runOrgSummary(t, root, "--fast")
	if m["fast"] != true || m["serve_running"] != false {
		t.Fatalf("payload = %v", m)
	}
	orgs := m["orgs"].([]interface{})
	if len(orgs) != 2 {
		t.Fatalf("orgs = %v", orgs)
	}
	acme := orgs[0].(map[string]interface{})
	if acme["name"] != "acme" || acme["queued"] != float64(2) || acme["running"] != false || acme["needs_you"] != nil {
		t.Fatalf("acme = %v", acme)
	}
	totals := m["totals"].(map[string]interface{})
	if totals["orgs"] != float64(2) || totals["queued"] != float64(2) || totals["needs_you"] != float64(0) {
		t.Fatalf("totals = %v", totals)
	}
}

func TestOrgSummaryNoOrgs(t *testing.T) {
	m := runOrgSummary(t, t.TempDir(), "--fast")
	if orgs := m["orgs"].([]interface{}); len(orgs) != 0 {
		t.Fatalf("orgs = %v", orgs)
	}
}
