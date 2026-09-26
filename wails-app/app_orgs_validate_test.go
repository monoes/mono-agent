package main

import (
	"errors"
	"strings"
	"testing"
)

// TestOrgValidateResult: the JSON report on stdout wins over the exit
// status, so an invalid org shows its problems, not "exit status 1".
func TestOrgValidateResult(t *testing.T) {
	exit1 := errors.New("exit status 1")
	invalid := []byte(`{"error":"monomind org validate growth: role writer reports to missing role lead","org":"growth","v":1,"valid":false,"warnings":[]}` + "\n")
	if err := orgValidateResult(invalid, []byte("org growth is invalid: …"), exit1); err == nil ||
		!strings.Contains(err.Error(), "reports to missing role lead") {
		t.Fatalf("invalid org (exit 1): %v", err)
	}
	// Older CLIs exited 0 with valid:false.
	if err := orgValidateResult(invalid, nil, nil); err == nil || !strings.Contains(err.Error(), "missing role") {
		t.Fatalf("invalid org (exit 0): %v", err)
	}
	if err := orgValidateResult([]byte(`{"valid":true,"org":"growth"}`), nil, nil); err != nil {
		t.Fatalf("valid org: %v", err)
	}
	if err := orgValidateResult([]byte(`{"valid":false}`), nil, exit1); err == nil || err.Error() != "invalid org config" {
		t.Fatalf("no message: %v", err)
	}
	// No report at all: the CLI's stderr, else the run error.
	if err := orgValidateResult(nil, []byte("unknown flag: --project\n"), exit1); err == nil || err.Error() != "unknown flag: --project" {
		t.Fatalf("no report: %v", err)
	}
	if err := orgValidateResult(nil, nil, exit1); err != exit1 {
		t.Fatalf("bare failure: %v", err)
	}
}
