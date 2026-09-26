package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeValidatingMonomind is a monomind stand-in whose `org validate`
// rejects the org named reject (exit 1) and accepts every other one.
func fakeValidatingMonomind(t *testing.T, reject string) {
	t.Helper()
	script := `#!/bin/sh
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.10.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'
  exit 0
fi
if [ "$1" = "org" ] && [ "$2" = "validate" ]; then
  if [ "$3" = "` + reject + `" ]; then echo "role writer reports to missing role lead" >&2; exit 1; fi
  echo "ok"; exit 0
fi
exit 0
`
	p := filepath.Join(t.TempDir(), "monomind")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOMIND_BIN", p)
}

// TestOrgValidateInvalidExitsNonZero: an invalid org prints exactly one
// JSON report ({valid:false}) and fails; main adds no second document.
func TestOrgValidateInvalidExitsNonZero(t *testing.T) {
	f := newOrgCLIFixture(t) // seeds "growth"
	fakeValidatingMonomind(t, "growth")

	cmd := newOrgCmd(f.cfg)
	cmd.SetArgs([]string{"validate", "growth"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr == nil {
		t.Fatalf("invalid org exited 0: %s", out)
	}
	if exitCodeFor(runErr) != 1 {
		t.Fatalf("exit code %d", exitCodeFor(runErr))
	}
	var rep reportedError
	if !errors.As(runErr, &rep) {
		t.Fatalf("not a reportedError: %T", runErr)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var v map[string]interface{}
	if err := dec.Decode(&v); err != nil || v["valid"] != false || !strings.Contains(v["error"].(string), "reports to missing role") || v["org"] != "growth" {
		t.Fatalf("stdout %q: %v", out, err)
	}
	if dec.More() {
		t.Fatalf("more than one JSON document: %q", out)
	}

	// main's error printing adds nothing on stdout for it (with --json),
	// but still does for an ordinary org error.
	var stdout, stderr bytes.Buffer
	reportCommandError([]string{"--json", "org", "validate", "growth"}, runErr, &stdout, &stderr)
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid") {
		t.Fatalf("reportedError: stdout %q stderr %q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	reportCommandError([]string{"--json", "org", "status"}, errors.New("boom"), &stdout, &stderr)
	if !strings.Contains(stdout.String(), `{"error":"boom"}`) {
		t.Fatalf("plain org error: stdout %q", stdout.String())
	}

	// A valid org still exits 0 with valid:true.
	fakeValidatingMonomind(t, "nothing")
	cmd = newOrgCmd(f.cfg)
	cmd.SetArgs([]string{"validate", "growth"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	out = captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil || !strings.Contains(out, `"valid":true`) {
		t.Fatalf("valid org: err %v out %s", runErr, out)
	}
}
