package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func eqArgs(t *testing.T, got []string, err error, want []string) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch\n got: %q\nwant: %q", got, want)
	}
}

func wantErr(t *testing.T, _ []string, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", contains)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err.Error(), contains)
	}
}

func TestAutomationArgs(t *testing.T) {
	a, err := automationListArgs("growth")
	eqArgs(t, a, err, []string{"automation", "list", "growth"})

	a, err = automationAddArgs("growth", "wf-1", "publish_post")
	eqArgs(t, a, err, []string{"automation", "add", "growth", "--workflow", "wf-1", "--alias", "publish_post"})

	a, err = automationAddArgs("growth", "wf-1", "Publish Post")
	wantErr(t, a, err, "invalid automation alias")

	a, err = automationAddArgs("growth", "", "publish")
	wantErr(t, a, err, "workflow id required")

	a, err = automationRemoveArgs("growth", "publish_post")
	eqArgs(t, a, err, []string{"automation", "remove", "growth", "--alias", "publish_post"})

	a, err = automationListArgs(" ")
	wantErr(t, a, err, "org name required")
}

func TestGrantAddArgs_Full(t *testing.T) {
	spec := `{"role":"lead","automation":"publish_post","mode":"run","wait":false,"timeout_seconds":900,"approval":"required","max_calls_per_run":5}`
	a, err := grantAddArgs("growth", spec)
	eqArgs(t, a, err, []string{
		"grant", "add", "growth", "--role", "lead", "--automation", "publish_post",
		"--mode", "run", "--wait=false", "--timeout", "900", "--approval", "required", "--max-calls-per-run", "5",
	})
}

func TestGrantAddArgs_MinimalLeavesDefaultsToCLI(t *testing.T) {
	a, err := grantAddArgs("growth", `{"role":"lead","automation":"publish_post"}`)
	eqArgs(t, a, err, []string{"grant", "add", "growth", "--role", "lead", "--automation", "publish_post"})
}

func TestGrantAddArgs_WaitTrueUsesEqualsForm(t *testing.T) {
	a, err := grantAddArgs("growth", `{"role":"lead","automation":"x","wait":true}`)
	eqArgs(t, a, err, []string{"grant", "add", "growth", "--role", "lead", "--automation", "x", "--wait=true"})
}

func TestGrantAddArgs_Rejects(t *testing.T) {
	cases := map[string]string{
		`{"automation":"x"}`:                                      "role required",
		`{"role":"lead","automation":"X"}`:                        "invalid automation alias",
		`{"role":"lead","automation":"x","mode":"fly"}`:           "mode must be",
		`{"role":"lead","automation":"x","approval":"ok"}`:        "approval must be",
		`{"role":"lead","automation":"x","timeout_seconds":0}`:    "timeout_seconds must be positive",
		`{"role":"lead","automation":"x","max_calls_per_run":-1}`: "max_calls_per_run must be positive",
		`not json`: "invalid grant spec",
	}
	for spec, contains := range cases {
		a, err := grantAddArgs("growth", spec)
		wantErr(t, a, err, contains)
	}
}

func TestGrantRemoveArgs(t *testing.T) {
	a, err := grantRemoveArgs("growth", "lead", "publish_post")
	eqArgs(t, a, err, []string{"grant", "remove", "growth", "--role", "lead", "--automation", "publish_post"})
	a, err = grantRemoveArgs("growth", "", "publish_post")
	wantErr(t, a, err, "role required")
}

func TestAutomationRoleArgs(t *testing.T) {
	a, err := automationRoleAddArgs("growth", `{"alias":"publisher-bot","reports_to":"lead","title":"Publisher","reply":"node:Format"}`)
	eqArgs(t, a, err, []string{
		"automation-role", "add", "growth", "--alias", "publisher-bot", "--reports-to", "lead",
		"--title", "Publisher", "--reply", "node:Format",
	})
	a, err = automationRoleAddArgs("growth", `{"alias":"publisher-bot"}`)
	wantErr(t, a, err, "reports_to required")
	a, err = automationRoleAddArgs("growth", `{"alias":"p","reports_to":"lead","reply":"first"}`)
	wantErr(t, a, err, "reply must be")

	a, err = automationRoleRemoveArgs("growth", "publisher-bot")
	eqArgs(t, a, err, []string{"automation-role", "remove", "growth", "--role", "publisher-bot"})

	a, err = effectiveToolsArgs("growth", "lead")
	eqArgs(t, a, err, []string{"effective-tools", "growth", "--role", "lead"})
}

func TestAutonomySetArgs_Full(t *testing.T) {
	spec := `{
	  "level":"mid",
	  "decider":{"kind":"model","runtime":"claude","model":"claude-fable-5-1","fallback":"model","timeout_seconds":120},
	  "policy":"Never approve spend over $50.",
	  "tiers":{"tool:WebFetch":"consequential","grant:publish_post":"irreversible"},
	  "clear_tiers":["tool:Bash"],
	  "on_decider_failure":"deny",
	  "limits":{"max_decisions_per_run":200,"max_decider_usd_per_run":2.5}
	}`
	a, err := autonomySetArgs("growth", spec)
	eqArgs(t, a, err, []string{
		"autonomy", "set", "growth", "--level", "mid",
		"--decider", "model", "--decider-runtime", "claude", "--decider-model", "claude-fable-5-1",
		"--fallback", "model", "--decider-timeout", "120",
		"--policy", "Never approve spend over $50.",
		"--tier", "grant:publish_post=irreversible", "--tier", "tool:WebFetch=consequential",
		"--clear-tier", "tool:Bash",
		"--on-decider-failure", "deny",
		"--max-decisions", "200", "--max-decider-usd", "2.5",
	})
}

func TestAutonomySetArgs_LevelOnly(t *testing.T) {
	a, err := autonomySetArgs("growth", `{"level":"full"}`)
	eqArgs(t, a, err, []string{"autonomy", "set", "growth", "--level", "full"})
}

func TestAutonomySetArgs_EmptyPolicyIsSentToClear(t *testing.T) {
	a, err := autonomySetArgs("growth", `{"policy":""}`)
	eqArgs(t, a, err, []string{"autonomy", "set", "growth", "--policy", ""})
}

func TestAutonomySetArgs_Rejects(t *testing.T) {
	cases := map[string]string{
		`{}`:                                "nothing to change",
		`{"level":"auto"}`:                  "level must be",
		`{"decider":{"kind":"ceo"}}`:        "decider kind must be",
		`{"decider":{"fallback":"boss"}}`:   "fallback must be model",
		`{"tiers":{"gate":"critical"}}`:     "tier for",
		`{"on_decider_failure":"retry"}`:    "on_decider_failure must be",
		`{"decider":{"timeout_seconds":0}}`: "timeout_seconds must be positive",
		`{"tiers":{"a=b":"routine"}}`:       "invalid decision class",
	}
	for spec, contains := range cases {
		a, err := autonomySetArgs("growth", spec)
		wantErr(t, a, err, contains)
	}
}

func TestAutonomyPauseResumeDecisionsNeedsYou(t *testing.T) {
	a, err := autonomyPauseArgs("growth", "30m")
	eqArgs(t, a, err, []string{"autonomy", "pause", "growth", "--for", "30m"})
	a, err = autonomyPauseArgs("growth", "2h")
	eqArgs(t, a, err, []string{"autonomy", "pause", "growth", "--for", "2h"})
	a, err = autonomyPauseArgs("growth", "")
	eqArgs(t, a, err, []string{"autonomy", "pause", "growth"})
	a, err = autonomyPauseArgs("growth", "forever")
	wantErr(t, a, err, "invalid pause duration")

	a, err = autonomyResumeArgs("growth")
	eqArgs(t, a, err, []string{"autonomy", "resume", "growth"})

	a, err = autonomyDecisionsArgs("growth", "")
	eqArgs(t, a, err, []string{"autonomy", "decisions", "growth"})
	a, err = autonomyDecisionsArgs("growth", "run-1")
	eqArgs(t, a, err, []string{"autonomy", "decisions", "growth", "--run", "run-1"})

	a, err = needsYouArgs("growth")
	eqArgs(t, a, err, []string{"autonomy", "needs-you", "growth"})
}

func TestGroupAndSendArgs(t *testing.T) {
	for _, verb := range []string{"start", "stop", "status"} {
		a, err := groupArgs(verb, "hq")
		eqArgs(t, a, err, []string{"group", verb, "hq"})
	}
	a, err := groupArgs("delete", "hq")
	wantErr(t, a, err, "unknown group action")
	a, err = groupArgs("start", "")
	wantErr(t, a, err, "holding org name required")

	a, err = sendArgs("sales", `{"to":"lead","from":"hq:ceo","subject":"Q3","body":"go"}`)
	eqArgs(t, a, err, []string{"send", "sales", "--to", "lead", "--from", "hq:ceo", "--subject", "Q3", "--body", "go"})
	a, err = sendArgs("sales", `{"to":"lead","body":"go"}`)
	wantErr(t, a, err, "subject required")
}

func TestOrgCLIArgs_GlobalFlagsBeforeOrg(t *testing.T) {
	got := orgCLIArgs("work", "/p/root", []string{"grant", "list", "growth"})
	want := []string{"--profile", "work", "--json", "org", "--project", "/p/root", "grant", "list", "growth"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	got = orgCLIArgs("", "", []string{"automation", "unassigned"})
	want = []string{"--json", "org", "automation", "unassigned"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := statusCLIArgs("work"); !reflect.DeepEqual(got, []string{"--profile", "work", "--json", "status"}) {
		t.Fatalf("status args %q", got)
	}
}

func TestCLIResultJSON(t *testing.T) {
	if got := cliResultJSON([]byte(" {\"v\":1}\n"), nil); got != `{"v":1}` {
		t.Fatalf("success passthrough: %q", got)
	}
	if got := cliResultJSON(nil, nil); !strings.Contains(got, "empty output") {
		t.Fatalf("empty: %q", got)
	}
	// The CLI's own {"error"} stdout payload wins over a bare exit error.
	if got := cliResultJSON([]byte(`{"error":"grant needs monomind"}`), errors.New("exit status 1")); got != `{"error":"grant needs monomind"}` {
		t.Fatalf("stdout error payload: %q", got)
	}
	if got := cliResultJSON([]byte("not json"), errors.New("exit status 2")); got != `{"error":"exit status 2"}` {
		t.Fatalf("fallback: %q", got)
	}
}

// TestNoOrgBindingImportsMonomind is the Go half of the Phase 5 lint gate
// (the vitest half lives in frontend/src/doctrine.test.js): org bindings go
// through `monoagentcli` subprocesses, never internal/monomind.
func TestNoOrgBindingImportsMonomind(t *testing.T) {
	files, err := filepath.Glob("app_org*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no org binding files found")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"github.com/monoes/mono-agent/internal/`+"monomind\"") {
			t.Errorf("%s imports internal/monomind; shell monoagentcli instead", f)
		}
	}
}
