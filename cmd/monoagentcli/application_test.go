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

	statusOut, err := runApplicationCmd(t, dbPath, "status", id, "set", "applied")
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
	if !strings.Contains(getOut2, "applied") || !strings.Contains(getOut2, "urgent") {
		t.Fatalf("expected updated status+tag in get output, got: %s", getOut2)
	}
}

// TestApplicationListTableShowsJobTitle exercises the plain-text table
// rendering path (JSONOutput: false) of `application list` and asserts the
// TITLE column shows the job's actual title, not its company name.
func TestApplicationListTableShowsJobTitle(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)

	runCmd := func(jsonOutput bool, args ...string) (string, error) {
		cfg := &globalConfig{DBPath: dbPath, JSONOutput: jsonOutput}
		cmd := newApplicationCmd(cfg)
		cmd.SetArgs(args)
		var out bytes.Buffer
		cmd.SetOut(&out)
		err := cmd.Execute()
		return out.String(), err
	}

	addOut, err := runCmd(true, "add", "--kind", "job", "--title", "Backend Engineer", "--company", "Acme", "--url", "https://acme.example/1")
	if err != nil {
		t.Fatalf("application add: %v (%s)", err, addOut)
	}

	listOut, err := runCmd(false, "list")
	if err != nil {
		t.Fatalf("application list: %v", err)
	}
	if !strings.Contains(listOut, "Backend Engineer") {
		t.Fatalf("expected list table TITLE column to show the job title, got: %s", listOut)
	}
	if strings.Contains(listOut, "Acme") {
		t.Fatalf("did not expect the company name in the list table output, got: %s", listOut)
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
