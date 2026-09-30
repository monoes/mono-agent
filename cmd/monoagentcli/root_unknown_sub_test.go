package main

import (
	"strings"
	"testing"
)

func TestUnknownSubcommandListsAvailable(t *testing.T) {
	root := newRootCmd()
	root.SetArgs([]string{"org", "show", "x"})
	root.SilenceUsage = true
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "available:") || !strings.Contains(err.Error(), "logs") {
		t.Fatalf("want error listing subcommands, got %v", err)
	}
}
