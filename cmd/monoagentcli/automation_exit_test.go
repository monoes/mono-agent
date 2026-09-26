package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/automation"
)

// TestAutomationValidateJSONExitCode (V1): ok:false exits non-zero with
// exactly one JSON document on stdout.
func TestAutomationValidateJSONExitCode(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "bad")
	os.MkdirAll(filepath.Join(dir, "actions"), 0o755)
	os.WriteFile(filepath.Join(dir, "automation.json"), []byte(`{"schema":"monoagent.automation/v1","id":"bad",
		"name":"B","version":"0.1.0","site":{"startUrl":"","domains":[]},"permissions":{"steps":["log"],"scripts":[]},
		"actions":["a"],"policy":{"tier":"standard"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "actions", "a.json"), []byte(`{"actionType":"a","automation":"bad","steps":[{"id":"s","type":"log"}]}`), 0o644)
	out, _, err := runAutomationCLI(t, home, "automation", "validate", dir, "--json")
	if err == nil {
		t.Fatalf("invalid package exited 0: %s", out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var v struct {
		OK     bool                   `json:"ok"`
		Issues []automation.IssueJSON `json:"issues"`
	}
	if derr := dec.Decode(&v); derr != nil || v.OK || len(v.Issues) == 0 {
		t.Fatalf("stdout %q: %v", out, derr)
	}
	if dec.More() {
		t.Fatalf("more than one JSON document: %q", out)
	}
}

// TestAutomationNewInstallRefusesExistingID (V2): `new --install` never
// overwrites an installed automation, and writes nothing when it refuses.
func TestAutomationNewInstallRefusesExistingID(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "hn")
	out, _, err := runAutomationCLI(t, home, "automation", "new", "hackernews", "--template", "basic",
		"--dir", dir, "--start-url", "https://acme.test/", "--install", "--json")
	if err == nil || !strings.Contains(out, "automation hackernews already exists") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if _, serr := os.Stat(dir); !os.IsNotExist(serr) {
		t.Fatalf("scaffold written despite the refusal: %v", serr)
	}
	// A fresh id still works, and a second --install of it is refused.
	fresh := filepath.Join(home, "acme")
	var created map[string]json.RawMessage
	mustJSON(t, home, &created, "automation", "new", "acme", "--template", "basic",
		"--dir", fresh, "--start-url", "https://acme.test/", "--install")
	out, _, err = runAutomationCLI(t, home, "automation", "new", "acme", "--template", "basic",
		"--dir", filepath.Join(home, "acme2"), "--start-url", "https://acme.test/", "--install", "--json")
	if err == nil || !strings.Contains(out, "already exists") {
		t.Fatalf("second new --install: err=%v out=%s", err, out)
	}
	// Without --install the id is not checked (scaffolding only).
	var only map[string]string
	mustJSON(t, home, &only, "automation", "new", "acme", "--template", "basic",
		"--dir", filepath.Join(home, "acme3"), "--start-url", "https://acme.test/")
}
