package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

// newAPITestDB is a migrated database and the HOME that goes with it.
func newAPITestDB(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MONOAGENT_API_CONTEXT_CONFINEMENT", "")
	return testdb.Path(t)
}

// runAPI runs `api <args>` and returns stdout and stderr separately.
func runAPI(t *testing.T, dbPath, profile string, jsonOut bool, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	// `revoke` asks only on a terminal; tests never have one.
	wasTerminal := apiStdinIsTerminal
	apiStdinIsTerminal = func() bool { return false }
	defer func() { apiStdinIsTerminal = wasTerminal }()

	cmd := newAPICmd(&globalConfig{DBPath: dbPath, ProfileID: profile, JSONOutput: jsonOut})
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestAPIKeyCreateHumanOutputIsTheKeyAloneOnStdout(t *testing.T) {
	db := newAPITestDB(t)
	stdout, stderr, err := runAPI(t, db, "default", false, "key", "create", "--name", "app")
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(stdout)
	if !strings.HasPrefix(key, apikeys.KeyPrefix) || strings.Contains(key, " ") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout must be the key alone so scripts can capture it, got %q", stdout)
	}
	if !strings.Contains(stderr, "only time") || strings.Contains(stderr, key) {
		t.Errorf("stderr must warn that the key is shown once, without repeating it: %q", stderr)
	}
}

func TestAPIKeyCreateValidationExitCodes(t *testing.T) {
	db := newAPITestDB(t)
	if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", ""); exitCode(err) != 3 {
		t.Errorf("empty name: exit %d (%v), want 3", exitCode(err), err)
	}
	if _, _, err := runAPI(t, db, "default", true, "key", "create"); err == nil {
		t.Error("a missing --name must be an error")
	}
	if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", "app"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", "app"); exitCode(err) != 3 {
		t.Errorf("duplicate name: exit %d (%v), want 3", exitCode(err), err)
	}
}

func TestAPIKeyListIsAnEmptyArrayNotNull(t *testing.T) {
	db := newAPITestDB(t)
	out, _, err := runAPI(t, db, "default", true, "key", "list")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("list on an empty profile: %q, %v; want []", out, err)
	}
}

func TestAPIKeyUpdateAndRevoke(t *testing.T) {
	db := newAPITestDB(t)
	if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", "app"); err != nil {
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "key", "update", "app", "--name", "app-2", "--context")
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]any
	_ = json.Unmarshal([]byte(out), &updated)
	if updated["name"] != "app-2" || updated["context"] != true {
		t.Fatalf("update: %v", updated)
	}
	out, _, _ = runAPI(t, db, "default", true, "key", "update", "app-2", "--no-context")
	_ = json.Unmarshal([]byte(out), &updated)
	if updated["context"] != false {
		t.Fatalf("--no-context: %v", updated)
	}
	if _, _, err := runAPI(t, db, "default", true, "key", "update", "app-2", "--context", "--no-context"); err == nil {
		t.Error("--context and --no-context together must be rejected")
	}
	if _, _, err := runAPI(t, db, "default", true, "key", "update", "ghost", "--context"); exitCode(err) != 2 {
		t.Errorf("update of an unknown key: exit %d, want 2", exitCode(err))
	}

	// Revoking needs --yes when stdin is not a terminal.
	if _, _, err := runAPI(t, db, "default", true, "key", "revoke", "app-2"); exitCode(err) != 3 {
		t.Errorf("revoke without --yes: exit %d (%v), want 3", exitCode(err), err)
	}
	out, _, err = runAPI(t, db, "default", true, "key", "revoke", "app-2", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	var revoked map[string]any
	_ = json.Unmarshal([]byte(out), &revoked)
	if revoked["revoked_at"] == nil {
		t.Fatalf("revoke: %v", revoked)
	}
	// Revoking is idempotent by id. By name it is not found afterwards: a
	// name belongs to active keys only, so the name is free again.
	id, _ := revoked["id"].(string)
	if _, _, err := runAPI(t, db, "default", true, "key", "revoke", id, "--yes"); err != nil {
		t.Errorf("revoking twice by id: %v", err)
	}
	if _, _, err := runAPI(t, db, "default", true, "key", "revoke", "app-2", "--yes"); exitCode(err) != 2 {
		t.Errorf("revoking a revoked key by name: exit %d, want 2", exitCode(err))
	}
	list, _, _ := runAPI(t, db, "default", true, "key", "list")
	if strings.TrimSpace(list) != "[]" {
		t.Errorf("a revoked key is still listed: %s", list)
	}
	withRevoked, _, _ := runAPI(t, db, "default", true, "key", "list", "--include-revoked")
	if !strings.Contains(withRevoked, "app-2") {
		t.Errorf("--include-revoked lost the key: %s", withRevoked)
	}
}

// The flags' own values count: --context=false must not turn context on.
func TestAPIKeyUpdateHonoursExplicitFlagValues(t *testing.T) {
	db := newAPITestDB(t)
	if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", "app", "--context"); err != nil {
		t.Fatal(err)
	}
	contextAfter := func(args ...string) bool {
		t.Helper()
		out, _, err := runAPI(t, db, "default", true, append([]string{"key", "update", "app"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var k apikeys.Key
		if err := json.Unmarshal([]byte(out), &k); err != nil {
			t.Fatal(err)
		}
		return k.Context
	}
	if contextAfter("--context=false") {
		t.Error("--context=false must turn context off")
	}
	if !contextAfter("--context=true") {
		t.Error("--context=true must turn context on")
	}
	if contextAfter("--no-context") {
		t.Error("--no-context must turn context off")
	}
	// --no-context=false is not "turn context on": on its own it changes
	// nothing, and next to a rename it leaves the context as it was.
	if _, _, err := runAPI(t, db, "default", true, "key", "update", "app", "--no-context=false"); exitCode(err) != 3 {
		t.Errorf("--no-context=false alone has nothing to change: exit %d (%v), want 3", exitCode(err), err)
	}
	if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", "plain"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runAPI(t, db, "default", true, "key", "update", "plain", "--no-context=false", "--name", "renamed")
	if err != nil {
		t.Fatal(err)
	}
	var k apikeys.Key
	if err := json.Unmarshal([]byte(out), &k); err != nil || k.Context {
		t.Errorf("--no-context=false turned context on: %+v (%v)", k, err)
	}
}

func TestAPIKeyCreateWithContextSaysWhichModelsServeIt(t *testing.T) {
	db := newAPITestDB(t)
	_, withNote, err := runAPI(t, db, "default", false, "key", "create", "--name", "notes", "--context")
	if err != nil || !strings.Contains(withNote, "chat-only") || !strings.Contains(withNote, "--context-confinement") {
		t.Errorf("a context key must say it is served by chat-only models unless the server raises --context-confinement: %q (%v)", withNote, err)
	}
	_, plainNote, err := runAPI(t, db, "default", false, "key", "create", "--name", "plain")
	if err != nil || strings.Contains(plainNote, "chat-only") {
		t.Errorf("a plain key needs no such note: %q (%v)", plainNote, err)
	}
}

func TestAPIKeysAreProfileScoped(t *testing.T) {
	db := newAPITestDB(t)
	work := "work"
	seedProfile(t, db, work)
	if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runAPI(t, db, work, true, "key", "create", "--name", "beta"); err != nil {
		t.Fatal(err)
	}

	list, _, _ := runAPI(t, db, "default", true, "key", "list")
	if strings.Contains(list, "beta") || !strings.Contains(list, "alpha") {
		t.Errorf("the default profile sees %s", list)
	}
	all, _, _ := runAPI(t, db, "default", true, "key", "list", "--all-profiles")
	if !strings.Contains(all, "alpha") || !strings.Contains(all, "beta") {
		t.Errorf("--all-profiles must list every profile: %s", all)
	}
	// Another profile's key is "not found" — never "forbidden".
	for _, args := range [][]string{{"key", "show", "beta"}, {"key", "update", "beta", "--context"}, {"key", "revoke", "beta", "--yes"}} {
		_, _, err := runAPI(t, db, "default", true, args...)
		if exitCode(err) != 2 || (err != nil && strings.Contains(strings.ToLower(err.Error()), "forbidden")) {
			t.Errorf("%v: exit %d (%v), want 2 and a plain not-found", args, exitCode(err), err)
		}
	}
}

func TestAPIKeyAuthenticatesAfterCreate(t *testing.T) {
	db := newAPITestDB(t)
	stdout, _, err := runAPI(t, db, "default", false, "key", "create", "--name", "app")
	if err != nil {
		t.Fatal(err)
	}
	d, err := storage.NewDatabase(db)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	key, err := apikeys.NewStore(d.DB).Authenticate(context.Background(), strings.TrimSpace(stdout))
	if err != nil || key.ProfileID != "default" {
		t.Fatalf("the key the CLI printed does not authenticate: %+v, %v", key, err)
	}
	if !errors.Is(func() error { _, e := apikeys.NewStore(d.DB).Authenticate(context.Background(), "nope"); return e }(), apikeys.ErrInvalidKey) {
		t.Error("garbage must not authenticate")
	}
}
