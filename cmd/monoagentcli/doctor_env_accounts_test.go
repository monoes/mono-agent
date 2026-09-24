package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/connections"
	"github.com/monoes/mono-agent/internal/health"
)

// The accounts checks only read. On a real, migrated database: testing a
// connection or an AI provider saves nothing (no status, no new label),
// prints nothing to stdout (doctor --json stays parseable), and no stored
// secret reaches the error text — Telegram's validator puts the bot token
// in the URL and Go's HTTP errors print the whole URL.
//
// It runs in a child process with HTTPS_PROXY/HTTP_PROXY pointing at a
// closed port: Go reads the proxy once per process, so every request then
// fails at the network step, with the full URL (the token) in the error,
// while the database stays usable.
func TestAccountChecksOnlyReadAndLeakNothing(t *testing.T) {
	if os.Getenv("ACCOUNT_CHECKS_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run", "^TestAccountChecksOnlyReadAndLeakNothing$", "-test.v")
		cmd.Env = append(os.Environ(), "ACCOUNT_CHECKS_CHILD=1",
			"HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "NO_PROXY=", "no_proxy=",
			"HOME="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestAccountChecksOnlyReadAndLeakNothing") {
			t.Fatalf("child run failed (%v):\n%s", err, out)
		}
		return
	}
	keyring.MockInit()
	home := os.Getenv("HOME")
	dbPath := filepath.Join(home, ".monoagent", "monoagent.db")
	db, err := initDB(&globalConfig{DBPath: dbPath, ProfileID: "default"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	const token = "123456:SUPERSECRETTOKEN"
	store := connections.NewStore(db.DB)
	if err := store.EnsureTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, &connections.Connection{ID: "c1", Platform: "telegram", Method: connections.MethodAPIKey,
		Label: "orig", Status: "untested", ProfileID: "default", Data: map[string]interface{}{"bot_token": token}}); err != nil {
		t.Fatal(err)
	}
	const apiKey = "sk-TESTKEY-must-not-leak-1234"
	aiStore, err := ai.NewAIStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err := aiStore.SaveProvider(ai.AIProvider{ID: "p1", Name: "mine", ProviderID: "openai", Tier: "known",
		APIKey: apiKey, BaseURL: "http://127.0.0.1:1", Status: "untested", ProfileID: "default"}); err != nil {
		t.Fatal(err)
	}

	env := &health.Env{ProfileID: "default"}
	addAccountHooks(env, db.DB)

	var connErr, aiErr error
	out := captureStdout(t, func() {
		connErr = env.TestConnection(ctx, "c1")
		aiErr = env.TestAIProvider(ctx, "p1")
	})
	if out != "" {
		t.Errorf("checks printed to stdout: %q", out)
	}
	if connErr == nil || strings.Contains(connErr.Error(), token) || strings.Contains(connErr.Error(), "SUPERSECRET") {
		t.Errorf("connection error %v: want a failure that does not name the token", connErr)
	}
	if aiErr != nil && strings.Contains(aiErr.Error(), apiKey) {
		t.Errorf("AI provider error names the key: %v", aiErr)
	}

	c, err := store.Get(ctx, "c1", "default")
	if err != nil || c.Label != "orig" || c.Status != "untested" || c.LastTested != "" {
		t.Errorf("connection changed by a check: %+v, %v", c, err)
	}
	p, err := aiStore.GetProvider("p1", "default")
	if err != nil || p.Status != "untested" {
		t.Errorf("AI provider status changed by a check: %q, %v", p.Status, err)
	}

	// A connection of another profile is not visible to this one's checks
	// or its refresh fix.
	if err := store.Save(ctx, &connections.Connection{ID: "other", Platform: "telegram", Method: connections.MethodOAuth,
		Label: "x", ProfileID: "work", Data: map[string]interface{}{"refresh_token": "rt-abcdefg"}}); err != nil {
		t.Fatal(err)
	}
	if err := env.RefreshConnection(ctx, "other"); err == nil || !strings.Contains(err.Error(), "not found in this profile") {
		t.Errorf("refresh of another profile's connection = %v, want refused", err)
	}
}

func TestScrubSecrets(t *testing.T) {
	err := scrubSecrets(os.ErrClosed, "short")
	if err != os.ErrClosed {
		t.Error("an error with nothing to scrub was replaced")
	}
	got := scrubSecrets(io.ErrUnexpectedEOF, "unexpected")
	if strings.Contains(got.Error(), "unexpected") {
		t.Errorf("scrubbed = %q", got)
	}
}
