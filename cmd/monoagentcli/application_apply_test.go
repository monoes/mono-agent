// cmd/monoagentcli/application_apply_test.go
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apply"
	"github.com/monoes/mono-agent/internal/documents"
)

func runApplicationApplyCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newApplicationCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func writeDataFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplicationApplyAutoModeSkipsPrompt(t *testing.T) {
	origPDF := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) { return []byte("%PDF-fake"), nil }
	t.Cleanup(func() { documents.RenderPDFFunc = origPDF })

	// This test's assertion is about auto-mode prompt suppression, not
	// browser mechanics (TestOpenForApplicationLaunchesBrowser in
	// internal/apply covers that separately) -- fake it out so the test
	// doesn't depend on a real, sandboxed Chrome launch succeeding in
	// whatever environment runs this suite.
	origOpen := apply.OpenForApplicationFunc
	apply.OpenForApplicationFunc = func(ctx context.Context, jobURL string) error { return nil }
	t.Cleanup(func() { apply.OpenForApplicationFunc = origOpen })

	origPrompt := confirmPromptFunc
	confirmPromptFunc = func(string) bool {
		t.Fatal("auto mode must never call the confirmation prompt")
		return false
	}
	t.Cleanup(func() { confirmPromptFunc = origPrompt })

	dbPath := newApplicationCLITestDB(t)
	addOut, err := runApplicationApplyCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "about:blank")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}
	var id string
	for _, tok := range strings.Split(addOut, `"`) {
		if len(tok) == 36 && strings.Count(tok, "-") == 4 {
			id = tok
			break
		}
	}

	cvFile := writeDataFile(t, `{"name":"Jane Doe"}`)
	letterFile := writeDataFile(t, `{"senderName":"Jane Doe"}`)
	out, err := runApplicationApplyCmd(t, dbPath, "apply", id, "--mode", "auto", "--cv-data-file", cvFile, "--cover-letter-data-file", letterFile)
	if err != nil {
		t.Fatalf("application apply: %v (%s)", err, out)
	}
}

func TestApplicationApplyConfirmModeDeclined(t *testing.T) {
	origPrompt := confirmPromptFunc
	confirmPromptFunc = func(string) bool { return false }
	t.Cleanup(func() { confirmPromptFunc = origPrompt })

	dbPath := newApplicationCLITestDB(t)
	addOut, err := runApplicationApplyCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "about:blank")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}
	var id string
	for _, tok := range strings.Split(addOut, `"`) {
		if len(tok) == 36 && strings.Count(tok, "-") == 4 {
			id = tok
			break
		}
	}

	cvFile := writeDataFile(t, `{"name":"Jane Doe"}`)
	letterFile := writeDataFile(t, `{"senderName":"Jane Doe"}`)
	out, err := runApplicationApplyCmd(t, dbPath, "apply", id, "--mode", "confirm", "--cv-data-file", cvFile, "--cover-letter-data-file", letterFile)
	if err != nil {
		t.Fatalf("application apply (declined): %v (%s)", err, out)
	}
	if !strings.Contains(out, "cancelled") && !strings.Contains(out, "Cancelled") {
		t.Fatalf("expected a cancellation message when the confirm prompt is declined, got: %s", out)
	}
}

func TestApplicationSendTransitionsToApplied(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	addOut, err := runApplicationApplyCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "about:blank")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}
	var id string
	for _, tok := range strings.Split(addOut, `"`) {
		if len(tok) == 36 && strings.Count(tok, "-") == 4 {
			id = tok
			break
		}
	}

	sendOut, err := runApplicationApplyCmd(t, dbPath, "send", id)
	if err != nil {
		t.Fatalf("application send: %v (%s)", err, sendOut)
	}

	getOut, err := runApplicationApplyCmd(t, dbPath, "get", id)
	if err != nil {
		t.Fatalf("application get: %v", err)
	}
	if !strings.Contains(getOut, "applied") {
		t.Fatalf("expected status applied after send, got: %s", getOut)
	}
}
