// cmd/monoagentcli/application_discover_test.go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func runApplicationDiscoverCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newApplicationCmd(cfg)
	cmd.SetArgs(append([]string{"discover"}, args...))
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestApplicationDiscoverRejectsUnknownSource(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	_, err := runApplicationDiscoverCmd(t, dbPath, "--keywords", "engineer", "--source", "does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown source, got nil")
	}
}

func TestApplicationDiscoverRequiresKeywords(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	_, err := runApplicationDiscoverCmd(t, dbPath)
	if err == nil {
		t.Fatal("expected error for missing --keywords, got nil")
	}
	if !strings.Contains(err.Error(), "keywords") {
		t.Fatalf("expected error to mention keywords, got: %v", err)
	}
}
