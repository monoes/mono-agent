// cmd/monoagentcli/application_test.go
package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func newApplicationCLITestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cli-application-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if err := db.DB.Close(); err != nil {
		t.Fatalf("closing seed db: %v", err)
	}
	return dbPath
}

func runApplicationCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newApplicationCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestApplicationAddListGetStatusTag(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)

	addOut, err := runApplicationCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "https://acme.example/1")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}
	if !strings.Contains(addOut, `"id"`) {
		t.Fatalf("expected JSON id in add output, got: %s", addOut)
	}
	// Extract id crudely (tests below just need list/get to find something).
	var id string
	for _, tok := range strings.Split(addOut, `"`) {
		if len(tok) == 36 && strings.Count(tok, "-") == 4 {
			id = tok
			break
		}
	}
	if id == "" {
		t.Fatalf("could not extract id from add output: %s", addOut)
	}

	listOut, err := runApplicationCmd(t, dbPath, "list")
	if err != nil {
		t.Fatalf("application list: %v", err)
	}
	if !strings.Contains(listOut, "Acme") {
		t.Fatalf("expected list output to contain company, got: %s", listOut)
	}

	getOut, err := runApplicationCmd(t, dbPath, "get", id)
	if err != nil {
		t.Fatalf("application get: %v (%s)", err, getOut)
	}
	if !strings.Contains(getOut, "pending") {
		t.Fatalf("expected get output to show pending status, got: %s", getOut)
	}

	// Uses "cancelled" (not "applied") here: pending->applied via the
	// generic `status set` command is the exact bug under test in
	// TestApplicationStatusRejectsAppliedStatus below -- this scenario only
	// cares about a valid, non-"applied" transition plus tagging.
	statusOut, err := runApplicationCmd(t, dbPath, "status", id, "set", "cancelled")
	if err != nil {
		t.Fatalf("application status set: %v (%s)", err, statusOut)
	}

	tagOut, err := runApplicationCmd(t, dbPath, "tag", id, "add", "urgent")
	if err != nil {
		t.Fatalf("application tag add: %v (%s)", err, tagOut)
	}

	getOut2, err := runApplicationCmd(t, dbPath, "get", id)
	if err != nil {
		t.Fatalf("application get (after status/tag): %v", err)
	}
	if !strings.Contains(getOut2, "cancelled") || !strings.Contains(getOut2, "urgent") {
		t.Fatalf("expected updated status+tag in get output, got: %s", getOut2)
	}
}

// TestApplicationStatusRejectsAppliedStatus covers the invariant that
// automation/generic status changes must never be able to mark an
// application "applied" -- only `application send` (an explicit,
// human-triggered action) may do that. Regression test for a bypass where
// `application status <id> set applied` shelled straight through to
// store.SetStatus with no restriction on the target status.
func TestApplicationStatusRejectsAppliedStatus(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	addOut, err := runApplicationCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "https://acme.example/1")
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
	if id == "" {
		t.Fatalf("could not extract id from add output: %s", addOut)
	}

	if _, err := runApplicationCmd(t, dbPath, "status", id, "set", "applied"); err == nil {
		t.Fatal("expected error setting status to \"applied\" via the generic status command, got nil")
	}
	// Case-insensitive variants must be rejected too.
	if _, err := runApplicationCmd(t, dbPath, "status", id, "set", "Applied"); err == nil {
		t.Fatal("expected error setting status to \"Applied\" via the generic status command, got nil")
	}

	getOut, err := runApplicationCmd(t, dbPath, "get", id)
	if err != nil {
		t.Fatalf("application get: %v (%s)", err, getOut)
	}
	if !strings.Contains(getOut, "pending") {
		t.Fatalf("expected application to remain pending after rejected \"applied\" transition, got: %s", getOut)
	}
	if strings.Contains(getOut, "\"applied\"") {
		t.Fatalf("application must not have been marked applied via generic status command, got: %s", getOut)
	}
}

func TestApplicationStatusRejectsInvalidTransition(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	addOut, err := runApplicationCmd(t, dbPath, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "https://acme.example/1")
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
	if _, err := runApplicationCmd(t, dbPath, "status", id, "set", "rejected"); err == nil {
		t.Fatal("expected error for pending->rejected, got nil")
	}
}

func TestApplicationGetNotFound(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	if _, err := runApplicationCmd(t, dbPath, "get", "does-not-exist"); err == nil {
		t.Fatal("expected error for unknown id, got nil")
	}
}
