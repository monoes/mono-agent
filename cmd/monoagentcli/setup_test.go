package main

import (
	"reflect"
	"testing"

	"github.com/monoes/mono-agent/internal/health"
)

func optRow(id, fixID string) health.Result {
	return health.Result{ID: id, Status: health.StatusInfo, Fix: &health.FixInfo{ID: fixID, Optional: true}}
}

func TestOptionalFixIDs(t *testing.T) {
	noRuntime := &health.Report{Results: []health.Result{
		{ID: health.CheckRuntimes, Status: health.StatusFail},
		optRow("runtimes.claude", "runtimes.install:claude"),
		optRow("runtimes.codex", "runtimes.install:codex"),
		optRow(health.CheckAutostart, health.FixAutostart),
		optRow(health.CheckMCP, health.FixMCPRegister),
	}}
	silent := func(string) string { return "" }

	// Non-interactive, no flags: nothing optional happens.
	if got := optionalFixIDs(noRuntime, setupWants{}, silent); len(got) != 0 {
		t.Fatalf("no flags: %v", got)
	}
	// Flags.
	got := optionalFixIDs(noRuntime, setupWants{runtimes: []string{"codex"}, autostart: true}, silent)
	if want := []string{"runtimes.install:codex", health.FixAutostart}; !reflect.DeepEqual(got, want) {
		t.Fatalf("flags: %v, want %v", got, want)
	}
	// Interactive answers.
	answers := func(q string) string {
		switch {
		case q[:2] == "No":
			return "claude"
		case q[:5] == "Start":
			return "y"
		}
		return "n"
	}
	got = optionalFixIDs(noRuntime, setupWants{}, answers)
	if want := []string{"runtimes.install:claude", health.FixAutostart}; !reflect.DeepEqual(got, want) {
		t.Fatalf("interactive: %v, want %v", got, want)
	}

	// A runtime is already installed: don't ask about runtimes again.
	has := &health.Report{Results: []health.Result{
		{ID: health.CheckRuntimes, Status: health.StatusOK},
		optRow("runtimes.codex", "runtimes.install:codex"),
	}}
	asked := false
	optionalFixIDs(has, setupWants{}, func(string) string { asked = true; return "" })
	if asked {
		t.Fatal("must not offer a runtime when one is installed")
	}
}
