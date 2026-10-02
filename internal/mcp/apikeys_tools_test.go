package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

// newAPIKeyServer is a server over a migrated copy of the test database (not a
// database migrated for this test, which costs seconds under the race detector),
// scoped to the default profile. It returns the database's path so a test can open a
// second handle to seed keys of other profiles and to see what was stored.
func newAPIKeyServer(t *testing.T, allowMutations bool) (*Server, string) {
	t.Helper()
	t.Setenv("MONOAGENT_MCP_ALLOW_MUTATIONS", "")
	dbPath := testdb.Path(t)
	s := NewServer(Options{
		DBPath:         dbPath,
		Profile:        "default",
		WorkflowsDir:   filepath.Join(t.TempDir(), "workflows"),
		Version:        "test",
		AllowMutations: allowMutations,
	})
	t.Cleanup(s.closeRuntime)
	return s, dbPath
}

// sideDB opens a second handle on the server's database file.
func sideDB(t *testing.T, dbPath string) *storage.Database {
	t.Helper()
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// callAPITool runs what a tools/call runs, in order, on a server that stays open:
// Serve answers every line on a goroutine of its own and closes the database when
// its input ends, so a sequence that depends on its own earlier calls cannot go
// through it.
func callAPITool(t *testing.T, s *Server, name string, args map[string]any) (string, error) {
	t.Helper()
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	return callTool(context.Background(), s, name, raw)
}

// mustCall is callAPITool for a call that must succeed.
func mustCall(t *testing.T, s *Server, name string, args map[string]any) string {
	t.Helper()
	text, err := callAPITool(t, s, name, args)
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return text
}

func decodeKeys(t *testing.T, text string) []map[string]any {
	t.Helper()
	var keys []map[string]any
	if err := json.Unmarshal([]byte(text), &keys); err != nil {
		t.Fatalf("not a list of keys: %v\n%s", err, scrubbed(text))
	}
	return keys
}

func namesOf(keys []map[string]any) []string {
	names := []string{}
	for _, k := range keys {
		names = append(names, k["name"].(string))
	}
	sort.Strings(names)
	return names
}

// The two reads are there without --allow-mutations, and carry annotations that
// say so.
func TestAPIReadOnlyToolsAreListedWithoutTheFlagAndAnnotated(t *testing.T) {
	s, _ := newAPIKeyServer(t, false)
	names := toolsListNames(t, s)
	want := map[string]map[string]bool{
		"api_key_list":    {"readOnlyHint": true, "idempotentHint": true},
		"api_models_list": {"readOnlyHint": true, "idempotentHint": true},
	}
	got := map[string]map[string]bool{}
	for _, def := range toolDefinitions(false) {
		if name := def["name"].(string); want[name] != nil {
			got[name], _ = def["annotations"].(map[string]bool)
		}
	}
	for name := range want {
		if !names[name] {
			t.Errorf("%s is read-only and must be listed without --allow-mutations", name)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("annotations\n got %v\nwant %v", got, want)
	}
}

func TestAPIKeyListOfAnEmptyProfileIsAnEmptyArray(t *testing.T) {
	s, _ := newAPIKeyServer(t, false)
	if text := mustCall(t, s, "api_key_list", nil); strings.TrimSpace(text) != "[]" {
		t.Errorf("api_key_list on a profile without keys = %q, want []", text)
	}
}

// The list is the metadata of this profile's keys and nothing else: no secret, no
// hash, and no key of another profile.
func TestAPIKeyListIsTheMetadataOfThisProfilesKeys(t *testing.T) {
	s, dbPath := newAPIKeyServer(t, false)
	ctx := context.Background()
	side := apikeys.NewStore(sideDB(t, dbPath).DB)
	_, alphaSecret, _ := side.Create(ctx, "default", "alpha", false)
	beta, betaSecret, _ := side.Create(ctx, "default", "beta", true)
	_, otherSecret, _ := side.Create(ctx, "other-profile", "gamma", false)
	if _, err := side.Revoke(ctx, "default", beta.ID); err != nil {
		t.Fatal(err)
	}

	text := mustCall(t, s, "api_key_list", nil)
	if got := namesOf(decodeKeys(t, text)); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Errorf("active keys of the default profile = %v, want [alpha]", got)
	}

	withRevoked := mustCall(t, s, "api_key_list", map[string]any{"include_revoked": true})
	keys := decodeKeys(t, withRevoked)
	if got := namesOf(keys); !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Errorf("include_revoked = %v, want alpha and beta, and not another profile's gamma", got)
	}
	for _, k := range keys {
		fields := []string{}
		for f := range k {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		wantFields := []string{"context", "created_at", "id", "last_used_at", "name", "prefix", "profile_id", "revoked_at"}
		if !reflect.DeepEqual(fields, wantFields) {
			t.Errorf("a key is described by %v, want exactly %v", fields, wantFields)
		}
		if k["profile_id"] != "default" {
			t.Errorf("profile_id = %v", k["profile_id"])
		}
		if k["name"] == "beta" && (k["context"] != true || k["revoked_at"] == nil) {
			t.Errorf("beta is a revoked key with context on: %v", k)
		}
	}

	for _, secret := range []string{alphaSecret, betaSecret, otherSecret} {
		for what, leaked := range map[string]string{"a key": secret, "a key's hash": apikeys.HashKey(secret), "a key without its prefix": secret[len(apikeys.KeyPrefix):]} {
			if strings.Contains(text+withRevoked, leaked) {
				t.Errorf("the list holds %s", what)
			}
		}
	}
}

func TestAPIKeyListRefusesAnArgumentOfTheWrongType(t *testing.T) {
	s, _ := newAPIKeyServer(t, false)
	if _, err := callAPITool(t, s, "api_key_list", map[string]any{"include_revoked": "yes"}); err == nil {
		t.Error("include_revoked must be a boolean")
	}
}
