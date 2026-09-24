package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeHTTP answers every request of this process with status, recording
// "METHOD host/path"; restored when the test ends.
func fakeHTTP(t *testing.T, status int) func() []string {
	t.Helper()
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	var mu sync.Mutex
	var seen []string
	http.DefaultTransport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Host+r.URL.Path)
		mu.Unlock()
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"error":"nope"}`)), Header: http.Header{}, Request: r}, nil
	})
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func accountTestDB(t *testing.T) (*health.Env, *connections.Store, *ai.AIStore) {
	t.Helper()
	keyring.MockInit()
	db, err := initDB(&globalConfig{DBPath: filepath.Join(t.TempDir(), "monoagent.db"), ProfileID: "default"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := connections.NewStore(db.DB)
	if err := store.EnsureTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	aiStore, err := ai.NewAIStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	env := &health.Env{ProfileID: "default"}
	addAccountHooks(env, db.DB)
	return env, store, aiStore
}

// connectionFixIDs runs the default connections check against env and
// returns each row's fix id by connection id (FixID is what the check
// sets; the runner resolves it).
func connectionFixIDs(t *testing.T, env *health.Env) map[string]string {
	t.Helper()
	for _, c := range health.Default().Checks() {
		if c.ID == health.CheckConnections {
			out := map[string]string{}
			for _, r := range c.Run(context.Background(), env).Children {
				out[strings.TrimPrefix(r.ID, "accounts.connection.")] = r.FixID
			}
			return out
		}
	}
	t.Fatal("no connections check")
	return nil
}

// The connection check offers the silent refresh only when the service
// refused the token or it expired; a server error gets no fix at all.
func TestConnectionFailuresAreClassified(t *testing.T) {
	env, store, _ := accountTestDB(t)
	ctx := context.Background()
	oauth := func(id, expires string) *connections.Connection {
		return &connections.Connection{ID: id, Platform: "github", Method: connections.MethodOAuth, ProfileID: "default",
			Data: map[string]interface{}{"access_token": "at-123456", "refresh_token": "rt-123456", "expires_at": expires}}
	}
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	for _, c := range []*connections.Connection{oauth("fresh", future), oauth("expired", past)} {
		if err := store.Save(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	fixFor := func(status int, id string) string {
		fakeHTTP(t, status)
		return connectionFixIDs(t, env)[id]
	}
	if f := fixFor(401, "fresh"); f != health.FixConnectionRefresh+":fresh" {
		t.Errorf("401: %q", f)
	}
	if f := fixFor(503, "fresh"); f != "" {
		t.Errorf("503: %q, want no fix", f)
	}
	if f := fixFor(404, "fresh"); f != health.FixConnectionTryRefresh+":fresh" {
		t.Errorf("404: %q", f)
	}
	// Past its expires_at: the refresh is the cure, whatever the status.
	if f := fixFor(404, "expired"); f != health.FixConnectionRefresh+":expired" {
		t.Errorf("expired: %q", f)
	}
}

// The AI check lists models (free) where that needs the key, and only
// falls back to the paid completion elsewhere.
func TestAIProviderCheckUsesFreeModelList(t *testing.T) {
	env, _, aiStore := accountTestDB(t)
	for _, p := range []ai.AIProvider{
		{ID: "oa", Name: "OpenAI", ProviderID: "openai", Tier: "known", APIKey: "sk-123456", ProfileID: "default"},
		{ID: "or", Name: "OpenRouter", ProviderID: "openrouter", Tier: "known", APIKey: "sk-654321", ProfileID: "default"},
	} {
		if err := aiStore.SaveProvider(p); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	seen := fakeHTTP(t, 401)
	if err := env.TestAIProvider(ctx, "oa"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("openai with a refused key: %v", err)
	}
	if got := seen(); len(got) != 1 || got[0] != "GET api.openai.com/v1/models" {
		t.Errorf("openai requests: %v, want only the model list", got)
	}
	seen = fakeHTTP(t, 401)
	_ = env.TestAIProvider(ctx, "or")
	if got := seen(); len(got) == 0 || !strings.HasPrefix(got[0], "POST openrouter.ai/api/v1/chat/completions") {
		t.Errorf("openrouter requests: %v, want the completion", got)
	}
}

func TestClassifyConnectionError(t *testing.T) {
	conn := &connections.Connection{Data: map[string]interface{}{}}
	now := time.Now()
	if classifyConnectionError(nil, nil, conn, now) != nil {
		t.Error("nil error")
	}
	timeout := &os.SyscallError{Syscall: "dial", Err: os.ErrDeadlineExceeded}
	for _, tc := range []struct {
		raw  error
		want string
	}{
		{&connections.StatusError{Code: 403}, "rejected"},
		{&connections.StatusError{Code: 502}, "unreachable"},
		{&connections.StatusError{Code: 429}, "unreachable"},
		{context.DeadlineExceeded, "unreachable"},
		{timeout, "unreachable"},
		{errors.New("validateX: missing token"), "unknown"},
	} {
		got := classifyConnectionError(tc.raw, tc.raw, conn, now)
		// Read back through the check, which is what reads the marks.
		env := &health.Env{
			Connections: func(context.Context) ([]health.ConnectionInfo, error) {
				return []health.ConnectionInfo{{ID: "c", Platform: "p", Method: "oauth", HasRefreshToken: true}}, nil
			},
			TestConnection: func(context.Context, string) error { return got },
		}
		kind := map[string]string{"": "unreachable", health.FixConnectionRefresh + ":c": "rejected",
			health.FixConnectionTryRefresh + ":c": "unknown"}[connectionFixIDs(t, env)["c"]]
		if kind != tc.want {
			t.Errorf("%v: %s, want %s", tc.raw, kind, tc.want)
		}
	}
}
