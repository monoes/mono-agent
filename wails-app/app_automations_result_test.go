package main

import (
	"errors"
	"strings"
	"testing"
)

// TestAutomationResultJSONKeepsFailedReport: a report printed by a command
// that exits 1 (a failed test/validate/verify) reaches the page intact.
func TestAutomationResultJSONKeepsFailedReport(t *testing.T) {
	report := `{"results":[{"action":"*","status":"fail","ok":false}]}`
	if got := automationResultJSON("/bin/monoagentcli", []byte(report+"\n"), errors.New("exit status 1")); got != report {
		t.Fatalf("failed report: %s", got)
	}
	if got := automationResultJSON("/bin/monoagentcli", []byte(`{"error":"boom"}`), errors.New("exit status 1")); got != `{"error":"boom"}` {
		t.Fatalf("error object: %s", got)
	}
	// Not JSON (or truncated): the usual error.
	if got := automationResultJSON("/bin/monoagentcli", []byte(`{"results":[`), errors.New("exit status 2")); !strings.Contains(got, `"error"`) {
		t.Fatalf("truncated: %s", got)
	}
	if got := automationResultJSON("/bin/monoagentcli", []byte(`{"ok":true}`), nil); got != `{"ok":true}` {
		t.Fatalf("success: %s", got)
	}
}
