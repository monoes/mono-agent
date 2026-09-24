package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestHealthArgs(t *testing.T) {
	if got, want := healthArgs("p1", true, true), []string{"--profile", "p1", "--json", "doctor", "--deep", "--projects"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("healthArgs = %v, want %v", got, want)
	}
	if got, want := healthArgs("", false, false), []string{"--json", "doctor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("healthArgs = %v, want %v", got, want)
	}
	if got, want := healthFixArgs("p1", "runtimes.install:claude"), []string{"--profile", "p1", "--json", "doctor", "fix", "--", "runtimes.install:claude"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("healthFixArgs = %v, want %v", got, want)
	}
}

func TestAgentInstallArgs(t *testing.T) {
	if got, want := agentInstallArgs("p1", "claude", true), []string{"--profile", "p1", "--json", "agent", "install", "claude", "--yes", "--force"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("agentInstallArgs = %v, want %v", got, want)
	}
	if got, want := agentInstallArgs("", "codex", false), []string{"--json", "agent", "install", "codex", "--yes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("agentInstallArgs = %v, want %v", got, want)
	}
}

func TestHealthReportJSONKeepsReportOnExit1(t *testing.T) {
	report := `{"v":1,"results":[]}`
	if got := healthReportJSON("monoagentcli", []byte(report+"\n"), errors.New("exit status 1")); got != report {
		t.Fatalf("report on a failing exit must be returned verbatim, got %s", got)
	}
	got := healthReportJSON("monoagentcli", []byte("Usage: monoagentcli …"), nil)
	if !strings.Contains(got, `"error"`) {
		t.Fatalf("non-report output must become an error, got %s", got)
	}
}
