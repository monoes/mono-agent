package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// An unknown subcommand of a grouping command must be an error, not that
// group's help printed to stdout with exit code 0 — every caller that parses
// our stdout (the GUI's Org tab, scripts) reads exit 0 as success and then
// chokes on the prose.
func TestUnknownSubcommandIsAnError(t *testing.T) {
	for _, args := range [][]string{
		// `autonomy` is what a pre-0.46 binary did not have; the GUI asked it
		// for `org autonomy needs-you <org>` and got `org`'s help back.
		{"org", "autonomy-typo", "needs-you", "acme"},
		{"org", "no-such-command"},
		{"org", "grant", "no-such-command"},
		{"workflow", "no-such-command"},
	} {
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		if err == nil {
			t.Fatalf("%v: want an error, got none (output %q)", args, out.String())
		}
		if !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("%v: want an unknown-command error, got %v", args, err)
		}
	}
}

// A grouping command invoked bare still prints its help, as before.
func TestBareGroupCommandStillPrintsHelp(t *testing.T) {
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"org"})
	if err := root.Execute(); err != nil {
		t.Fatalf("bare org: %v", err)
	}
	if !strings.Contains(out.String(), "Available Commands:") {
		t.Fatalf("bare org printed %q", out.String())
	}
}

// Guard the traversal itself: commands that do their own work keep their own
// argument handling, only pure groups get NoArgs.
func TestRejectUnknownSubcommandsLeavesRunnableCommandsAlone(t *testing.T) {
	parent := &cobra.Command{Use: "p", Run: func(*cobra.Command, []string) {}}
	parent.AddCommand(&cobra.Command{Use: "c", Run: func(*cobra.Command, []string) {}})
	group := &cobra.Command{Use: "g"}
	group.AddCommand(&cobra.Command{Use: "d", Run: func(*cobra.Command, []string) {}})
	custom := &cobra.Command{Use: "x", Args: cobra.ExactArgs(2)}
	custom.AddCommand(&cobra.Command{Use: "e", Run: func(*cobra.Command, []string) {}})
	rootStub := &cobra.Command{Use: "root"}
	rootStub.AddCommand(parent, group, custom)

	rejectUnknownSubcommands(rootStub)

	if parent.Args != nil {
		t.Fatal("runnable parent should keep its default args")
	}
	if group.Args == nil {
		t.Fatal("pure group should have been given NoArgs")
	}
	if err := custom.Args(custom, []string{"a", "b"}); err != nil {
		t.Fatalf("explicit Args was overwritten: %v", err)
	}
}

// wantsJSONError must look past the *values* of global flags: the GUI builds
// `--profile p --json org …`, where a naive "first non-dash arg" scan reads
// the profile id as the subcommand and the failure never reaches the page as
// JSON (wails-app/app_org_unification.go orgCLIArgs).
func TestWantsJSONErrorSkipsGlobalFlagValues(t *testing.T) {
	yes := [][]string{
		{"--json", "org", "list"},
		{"--profile", "p1", "--json", "org", "autonomy", "needs-you", "acme"},
		{"--profile=p1", "--json", "status"},
		{"--db-path", "/tmp/x.db", "--lang", "en", "--json", "status"},
		{"--workers", "2", "--json", "org", "list"},
	}
	for _, args := range yes {
		if !wantsJSONError(args) {
			t.Fatalf("%v: want JSON error output", args)
		}
	}
	no := [][]string{
		{"org", "list"},                                    // no --json
		{"--json", "workflow", "run", "x"},                 // not an org/status command
		{"--profile", "org", "workflow", "list", "--json"}, // a profile literally named "org"
	}
	for _, args := range no {
		if wantsJSONError(args) {
			t.Fatalf("%v: want no JSON error output", args)
		}
	}
}
