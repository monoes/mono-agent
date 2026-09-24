package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

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
	if got := optionalFixIDs(noRuntime, setupWants{}, silent); len(got.ids) != 0 || len(got.skipped) != 0 {
		t.Fatalf("no flags: %+v", got)
	}
	// Flags.
	got := optionalFixIDs(noRuntime, setupWants{runtimes: []string{"codex"}, autostart: true}, silent)
	if want := []string{"runtimes.install:codex", health.FixAutostart}; !reflect.DeepEqual(got.ids, want) {
		t.Fatalf("flags: %v, want %v", got.ids, want)
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
	if want := []string{"runtimes.install:claude", health.FixAutostart}; !reflect.DeepEqual(got.ids, want) {
		t.Fatalf("interactive: %v, want %v", got.ids, want)
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

// An extra that was asked for but won't be done is reported as skipped,
// with why, instead of being dropped (or reported as applied).
func TestOptionalExtrasReportSkipped(t *testing.T) {
	rep := &health.Report{Results: []health.Result{
		{ID: health.CheckRuntimes, Status: health.StatusOK},
		{ID: "runtimes.claude", Parent: health.CheckRuntimes, Status: health.StatusOK, Summary: "2.0"},
		{ID: "runtimes.codex", Parent: health.CheckRuntimes, Status: health.StatusInfo,
			Fix: &health.FixInfo{ID: "runtimes.install:codex", Optional: true}},
		{ID: "runtimes.cursor", Parent: health.CheckRuntimes, Status: health.StatusInfo, Detail: "see cursor.com"},
		{ID: health.CheckAutostart, Status: health.StatusOK, Summary: "systemd user service"},
		{ID: health.CheckMCP, Status: health.StatusSkip, Summary: "Claude Code not installed"},
	}}
	got := optionalFixIDs(rep, setupWants{runtimes: []string{"claude", "codex", "cursor", "claud"}, autostart: true, mcp: true},
		func(string) string { return "" })
	if want := []string{"runtimes.install:codex"}; !reflect.DeepEqual(got.ids, want) {
		t.Errorf("ids %v, want %v", got.ids, want)
	}
	reasons := map[string]string{}
	for _, o := range got.skipped {
		if o.Outcome != "skipped" {
			t.Errorf("%s: outcome %q", o.ID, o.Outcome)
		}
		reasons[o.ID] = o.Reason
	}
	for id, want := range map[string]string{
		"runtimes.install:claude": "already installed",
		"runtimes.install:cursor": "see cursor.com",
		"runtimes.install:claud":  `unknown runtime "claud" (can be installed: codex)`,
		health.FixAutostart:       "already set up (systemd user service)",
		health.FixMCPRegister:     "Claude Code not installed",
	} {
		if !strings.Contains(reasons[id], want) {
			t.Errorf("%s: reason %q, want %q", id, reasons[id], want)
		}
	}

	// A typo at the runtime prompt is reported too.
	none := &health.Report{Results: []health.Result{
		{ID: health.CheckRuntimes, Status: health.StatusFail},
		{ID: "runtimes.codex", Parent: health.CheckRuntimes, Status: health.StatusInfo,
			Fix: &health.FixInfo{ID: "runtimes.install:codex", Optional: true}},
	}}
	got = optionalFixIDs(none, setupWants{}, func(q string) string {
		if strings.HasPrefix(q, "No") {
			return "codx, codex"
		}
		return ""
	})
	if len(got.ids) != 1 || got.ids[0] != "runtimes.install:codex" || len(got.skipped) != 1 ||
		!strings.Contains(got.skipped[0].Reason, `unknown runtime "codx"`) {
		t.Errorf("typo: %+v", got)
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

// runCLI runs `monoagentcli <args>` against a throwaway HOME, stdout and
// stderr apart.
func runCLI(t *testing.T, home string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append(args, "--db-path", filepath.Join(home, ".monoagent", "monoagent.db")))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// Plan row 7: setup takes an empty HOME to a healthy core group in one
// run, announcing each stage and fix on stderr, and its first check sees
// the HOME as it was (no pre-run made the data folder first).
func TestSetupEmptyHomeToHealthyCore(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()

	out, errOut, err := runCLI(t, home, "--json", "setup", "--group", "core", "--yes")
	if err != nil {
		t.Fatalf("setup: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	var rep setupJSON
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stdout is not the report: %v\n%s", err, out)
	}
	for _, r := range rep.Results {
		if r.Group != health.GroupCore {
			t.Errorf("--group core ran %s", r.ID)
		}
		if r.Status == health.StatusFail {
			t.Errorf("%s still fails: %s", r.ID, r.Summary)
		}
	}
	for _, id := range []string{health.CheckHome, health.CheckDB, health.CheckProfile} {
		if st := resultByID(doctorJSON{Report: rep.Report}, id).Status; st != health.StatusOK {
			t.Errorf("%s: %q", id, st)
		}
	}
	var applied []string
	for _, f := range rep.Fixes {
		if f.Outcome == "applied" {
			applied = append(applied, f.ID)
		}
	}
	// core.home.create applied means the first check found no data folder:
	// the root's pre-run (which makes one) did not run before it.
	if want := []string{health.FixHomeCreate, health.FixDBMigrate, health.FixProfileLayout}; !reflect.DeepEqual(applied, want) {
		t.Errorf("applied %v, want %v", applied, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		t.Error("setup touched ~/.claude")
	}

	var events []fixEvent
	for _, l := range strings.Split(strings.TrimSpace(errOut), "\n") {
		if !strings.HasPrefix(l, "{") {
			continue // log lines
		}
		var ev fixEvent
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("bad event %q", l)
		}
		events = append(events, ev)
	}
	var stages, starts []string
	ended := map[string]string{}
	for _, ev := range events {
		switch ev.Kind {
		case "stage":
			stages = append(stages, ev.Message)
		case "fix_start":
			if _, done := ended[ev.FixID]; done {
				t.Errorf("fix_start after fix_end for %s", ev.FixID)
			}
			starts = append(starts, ev.FixID)
		case "fix_end":
			ended[ev.FixID] = ev.Outcome
		}
	}
	if want := []string{"Checking this machine", "Fixing what is missing"}; !reflect.DeepEqual(stages, want) {
		t.Errorf("stages %v, want %v", stages, want)
	}
	if !reflect.DeepEqual(starts, applied) {
		t.Errorf("fix_start %v, want %v", starts, applied)
	}
	for _, id := range applied {
		if ended[id] != "applied" {
			t.Errorf("fix_end for %s: %q", id, ended[id])
		}
	}

	// Again: nothing left to do.
	out, _, err = runCLI(t, home, "--json", "setup", "--group", "core", "--yes")
	if err != nil {
		t.Fatalf("second setup: %v", err)
	}
	var again setupJSON
	if err := json.Unmarshal([]byte(out), &again); err != nil || len(again.Fixes) != 0 {
		t.Errorf("second run fixes: %+v (%v)", again.Fixes, err)
	}
}

// doctor --fix's loop against the real core checks: each fix unblocks the
// next check (data folder → database → profile folder), across passes.
func TestFixUntilStableRealChecks(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := &globalConfig{DBPath: filepath.Join(home, ".monoagent", "monoagent.db")}
	reg := health.Default()
	opts := health.Options{Groups: []string{health.GroupCore}}
	ctx := context.Background()

	rep := runHealth(ctx, cfg, reg, opts)
	var lines []string
	rep, outcomes := fixUntilStable(ctx, cfg, reg, opts, rep, func(health.FixInfo) bool { return false },
		func(l string) { lines = append(lines, l) })
	var ids []string
	for _, o := range outcomes {
		if o.Outcome != "applied" {
			t.Errorf("%s: %s %s", o.ID, o.Outcome, o.Error)
		}
		ids = append(ids, o.ID)
	}
	if want := []string{health.FixHomeCreate, health.FixDBMigrate, health.FixProfileLayout}; !reflect.DeepEqual(ids, want) {
		t.Errorf("fixes %v, want %v", ids, want)
	}
	if n := rep.RequiredFailures(); n != 0 {
		t.Errorf("%d required failures after fixing", n)
	}
	if len(lines) == 0 || lines[0] != "→ Create data folder" {
		t.Errorf("progress: %v", lines)
	}
}

// A check that offers a new fix every time can't keep setup busy forever,
// and the stop is said out loud.
func TestSetupFixesSaysWhenThePassLimitStopsIt(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := &globalConfig{DBPath: filepath.Join(home, ".monoagent", "monoagent.db"), JSONOutput: true}
	n := 0
	reg := health.NewRegistry([]health.Check{{ID: "t.endless", Group: "t", Title: "Endless",
		Run: func(context.Context, *health.Env) health.Result {
			n++
			return health.Result{Status: health.StatusWarn, Summary: "more", FixID: fmt.Sprintf("t.fix:%d", n)}
		}}},
		[]health.Fix{{FixInfo: health.FixInfo{ID: "t.fix", Label: "Fix {arg}", Safety: health.SafetyAuto},
			ApplyArg: func(context.Context, *health.Env, string, func(string)) error { return nil }}})
	ctx := context.Background()
	var buf bytes.Buffer
	ev := setupEvents{w: &buf, json: true}
	_, outcomes := setupFixes(ctx, cfg, reg, health.Options{}, runHealth(ctx, cfg, reg, health.Options{}),
		func(health.FixInfo) bool { return false }, ev)
	if len(outcomes) != maxFixPasses+1 {
		t.Errorf("%d fixes, want %d", len(outcomes), maxFixPasses+1)
	}
	if !strings.Contains(buf.String(), "stopped after 5 passes") {
		t.Errorf("no word about the pass limit:\n%s", buf.String())
	}
	if c := strings.Count(buf.String(), `"kind":"fix_start","message":"Fix`); c != maxFixPasses+1 {
		t.Errorf("%d fix_start events:\n%s", c, buf.String())
	}
}

// setup's prompts and fixPrompter's read one buffer: given a
// *bufio.Reader, bufio.NewReader (what fixPrompter calls on InOrStdin)
// returns it as is, so the answer to one prompt isn't lost in the other's
// buffer.
func TestSetupSharesOneStdinReader(t *testing.T) {
	cmd := newSetupCmd(&globalConfig{})
	in := bufio.NewReader(strings.NewReader("y\nclaude\n"))
	cmd.SetIn(in)
	if bufio.NewReader(cmd.InOrStdin()) != in {
		t.Fatal("a second reader would wrap stdin")
	}
}
