package main

import (
	"bytes"
	"os"
	"reflect"
	"strings"
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
		case strings.HasPrefix(q, "No"):
			return "claude"
		case strings.HasPrefix(q, "Start"):
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

// Before anyone picks a runtime, the prompt names what each one runs: a
// vendor script's URL, or the npm package.
func TestRuntimePromptNamesWhatRuns(t *testing.T) {
	rep := &health.Report{Results: []health.Result{
		{ID: health.CheckRuntimes, Status: health.StatusFail},
		{ID: "runtimes.hermes", Status: health.StatusInfo, Fix: &health.FixInfo{ID: "runtimes.install:hermes", Optional: true,
			Command: "downloads and runs the vendor installer https://hermes.example/install.sh with bash"}},
		{ID: "runtimes.codex", Status: health.StatusInfo, Fix: &health.FixInfo{ID: "runtimes.install:codex", Optional: true,
			Command: "npm install -g @openai/codex"}},
	}}
	var asked string
	optionalFixIDs(rep, setupWants{}, func(q string) string {
		if strings.HasPrefix(q, "No") {
			asked = q
		}
		return ""
	})
	for _, want := range []string{"https://hermes.example/install.sh", "npm install -g @openai/codex"} {
		if !strings.Contains(asked, want) {
			t.Errorf("runtime prompt %q does not name %q", asked, want)
		}
	}
}

// A confirm fix nobody accepted is listed as not done, with how to accept
// it, so a run with no terminal does not look complete.
func TestManualStepsListDeclinedFixes(t *testing.T) {
	rep := &health.Report{Results: []health.Result{
		{ID: "a", Title: "Workflow daemon", Status: health.StatusWarn, Fix: &health.FixInfo{ID: "services.daemon.start", Label: "Start the workflow daemon", Safety: health.SafetyConfirm}},
		{ID: "b", Title: "Extension", Status: health.StatusWarn, Fix: &health.FixInfo{ID: "x", Command: "load it in the browser", Safety: health.SafetyManual}},
		{ID: "c", Title: "Runtime", Status: health.StatusInfo, Fix: &health.FixInfo{ID: "runtimes.install:qwen", Safety: health.SafetyConfirm, Optional: true}},
	}}
	var out bytes.Buffer
	printManualSteps(&out, rep)
	got := out.String()
	for _, want := range []string{"Start the workflow daemon", "--yes", "load it in the browser"} {
		if !strings.Contains(got, want) {
			t.Errorf("manual steps %q lack %q", got, want)
		}
	}
	if strings.Contains(got, "qwen") {
		t.Errorf("an optional extra is listed as not done: %q", got)
	}
}

// Service fixes wait while anything else is being fixed.
func TestWithoutServiceFixes(t *testing.T) {
	rep := &health.Report{Results: []health.Result{
		{ID: "core.db", Fix: &health.FixInfo{ID: "core.db.migrate"}},
		{ID: "services.daemon", Fix: &health.FixInfo{ID: health.FixDaemonStart}},
	}}
	held := withoutServiceFixes(rep)
	if held.Results[0].Fix == nil || held.Results[1].Fix != nil {
		t.Fatalf("held view: %+v", held.Results)
	}
	if rep.Results[1].Fix == nil {
		t.Fatal("the original report was modified")
	}
}

// /dev/null is a character device but nobody answers on it.
func TestStdinIsTerminalRejectsDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	orig := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = orig }()
	if stdinIsTerminal() {
		t.Fatal("stdin from /dev/null counted as a terminal")
	}
}
