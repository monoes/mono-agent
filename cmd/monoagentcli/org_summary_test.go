package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// The per-org budget holds even when monomind runs behind a wrapper whose
// child keeps stdout open past the kill (e.g. an npm .cmd shim).
func TestOrgSummaryNeedsYouTimeoutHolds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(t.TempDir(), "monomind")
	script := "#!/bin/sh\ncase \"$*\" in\n  *--version*) echo '{\"v\":1,\"version\":\"9.9.9\",\"capabilities\":[\"agent-exec\",\"agent-scan\",\"org-json-v1\"]}' ;;\n  *) sleep 6; echo '[]' ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOMIND_BIN", bin)
	old := perOrgNeedsYouTimeout
	perOrgNeedsYouTimeout = time.Second
	t.Cleanup(func() { perOrgNeedsYouTimeout = old })

	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "o.db"), ProfileID: "default"}
	env := &orgEnv{cfg: cfg}
	root := env.Root()
	writeOrgFile(t, root, "acme.json", `{"name":"acme"}`)
	cmd := newOrgSummaryCmd(env)
	var runErr error
	start := time.Now()
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	env.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("org summary took %v with a 1s per-org budget", took)
	}
	var m struct {
		Orgs []struct {
			NeedsYou      *int   `json:"needs_you"`
			NeedsYouError string `json:"needs_you_error"`
		} `json:"orgs"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil || len(m.Orgs) != 1 {
		t.Fatalf("out = %s (%v)", out, err)
	}
	if m.Orgs[0].NeedsYou != nil || !strings.Contains(m.Orgs[0].NeedsYouError, "timed out") {
		t.Fatalf("want a timeout error, got %+v", m.Orgs[0])
	}
}
