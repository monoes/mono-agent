package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/storage"
)

func TestPlanCost(t *testing.T) {
	cases := []struct {
		plan agentroster.Plan
		want string
	}{
		{agentroster.Plan{EstCostUSD: 0.0123}, "≈ $0.0123"},
		{agentroster.Plan{EstCostUSD: 0.2, TableEstimated: 12}, "≈ $0.2000 (12 priced from the built-in table)"},
		{agentroster.Plan{EstCostUSD: 0.2, TableEstimated: 2, UnknownCost: 3}, "≈ $0.2000 (2 priced from the built-in table; + 3 with unknown cost)"},
		{agentroster.Plan{UnknownCost: 3}, "≈ <$0.0001 (+ 3 with unknown cost)"},
	}
	for _, c := range cases {
		if got := planCost(&c.plan); got != c.want {
			t.Errorf("planCost(%+v) = %q, want %q", c.plan, got, c.want)
		}
	}
}

// The text output shows the sign-in note for the plan and the login hint on
// an auth result (monoes/mono-agent#271, #272).
func TestValidateEmitterShowsSignIn(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	var out string
	func() {
		defer func() { os.Stderr = orig }()
		out = captureStdout(t, func() {
			emit := validateEmitter(false)
			emit(agentroster.Line{Type: "validate.plan", Plan: &agentroster.Plan{Calls: 5, EstCostUSD: 0.1, TableEstimated: 5,
				SignIn: []agentroster.SignInNote{{Runtime: "claude", LoginHint: "claude /login"}}}})
			emit(agentroster.Line{Type: "validate.result", Result: &agentroster.Result{Runtime: "claude", Model: "opus",
				Status: agentroster.StatusAuth, Detail: "Not logged in", LoginHint: "claude /login"}})
		})
	}()
	w.Close()
	errOut, _ := io.ReadAll(r)
	if !strings.Contains(string(errOut), "claude isn't signed in, so its model list may be incomplete; sign in with: claude /login") {
		t.Errorf("stderr = %q", errOut)
	}
	if !strings.Contains(out, "5 test calls, ≈ $0.1000 (5 priced from the built-in table)") || !strings.Contains(out, "(sign in: claude /login)") {
		t.Errorf("stdout = %q", out)
	}
}

// `agent roster` shows an auth row's login hint in the table and the JSON.
func TestAgentRosterShowsLoginHint(t *testing.T) {
	dbPath := newStatusCLITestDB(t)
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	err = agentroster.Save(context.Background(), db.DB, agentroster.Result{Runtime: "claude", Model: "opus",
		Status: agentroster.StatusAuth, Detail: "Not logged in", LoginHint: "claude /login", ValidatedAt: time.Now()})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, jsonOut := range []bool{false, true} {
		cfg := &globalConfig{DBPath: dbPath, ProfileID: "default", JSONOutput: jsonOut}
		cmd := newAgentRosterCmd(cfg)
		cmd.SetArgs([]string{"--no-scan"})
		out := captureStdout(t, func() {
			if err := cmd.Execute(); err != nil {
				t.Errorf("roster: %v", err)
			}
		})
		want := "claude /login"
		if jsonOut {
			want = `"login_hint": "claude /login"`
		}
		if !strings.Contains(out, want) || (!jsonOut && !strings.Contains(out, "SIGN IN")) {
			t.Errorf("json=%v: output = %q, want %q", jsonOut, out, want)
		}
	}
}
