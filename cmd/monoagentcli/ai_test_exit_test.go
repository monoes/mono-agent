package main

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestAIProviderTestJSONExitCode: a failing provider test prints one JSON
// document with status "error" and exits non-zero.
func TestAIProviderTestJSONExitCode(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	db := filepath.Join(home, "ai.db")
	run := func(args ...string) (string, error) {
		var err error
		out := captureStdout(t, func() {
			root := newRootCmd()
			root.SetErr(io.Discard)
			root.SetArgs(append(args, "--db-path", db, "--json"))
			err = root.Execute()
		})
		return out, err
	}
	out, err := run("ai", "provider", "add", "--name", "Dead", "--provider", "openai", "--tier", "gateway",
		"--base-url", "http://127.0.0.1:1/v1", "--api-key", "k", "--model", "m")
	if err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	var p struct{ ID string }
	if err := json.Unmarshal([]byte(out), &p); err != nil || p.ID == "" {
		t.Fatalf("add output %q: %v", out, err)
	}
	out, err = run("ai", "provider", "test", p.ID)
	if err == nil {
		t.Fatalf("failing provider test exited 0: %s", out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var res map[string]string
	if derr := dec.Decode(&res); derr != nil || res["status"] != "error" || res["error"] == "" {
		t.Fatalf("stdout %q: %v", out, derr)
	}
	if dec.More() {
		t.Fatalf("more than one JSON document: %q", out)
	}
}
