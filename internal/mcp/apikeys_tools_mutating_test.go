package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apikeys"
)

// A key is never printed, not even a throwaway one in a failing test: whatever a
// failure message shows of a tool's answer goes through scrubbed.
var keyRE = regexp.MustCompile(`sk-ma-[A-Za-z0-9_-]{43}`)

func scrubbed(s string) string { return keyRE.ReplaceAllString(s, "sk-ma-<redacted>") }

func decodeKey(t *testing.T, text string) map[string]any {
	t.Helper()
	var k map[string]any
	if err := json.Unmarshal([]byte(text), &k); err != nil {
		t.Fatalf("not a key: %v\n%s", err, scrubbed(text))
	}
	return k
}

// describe is a key's fields for a failure message.
func describe(k map[string]any) string { return scrubbed(fmt.Sprint(k)) }

// stringField reads a field that must be there, without a type assertion that panics.
func stringField(t *testing.T, k map[string]any, field string) string {
	t.Helper()
	v, _ := k[field].(string)
	if v == "" {
		t.Fatalf("the result has no %q: its fields are %v", field, sortedFields(k))
	}
	return v
}

// createKey makes a key through the tool and returns its id and the key itself.
func createKey(t *testing.T, s *Server, name string, withContext bool) (id, secret string) {
	t.Helper()
	k := decodeKey(t, mustCall(t, s, "api_key_create", map[string]any{"name": name, "context": withContext}))
	return stringField(t, k, "id"), stringField(t, k, "key")
}

func keyRows(t *testing.T, dbPath string) int {
	t.Helper()
	var n int
	if err := sideDB(t, dbPath).DB.QueryRow(`SELECT COUNT(*) FROM api_keys`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sortedFields(m map[string]any) []string {
	fields := []string{}
	for f := range m {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	return fields
}

// The three tools that change something exist only with --allow-mutations, like the
// other mutating tools: omitted from tools/list, and refused by name without
// having done anything.
func TestAPIMutatingToolsAreGatedLikeTheOtherMutatingTools(t *testing.T) {
	mutating := []string{"api_key_create", "api_key_update", "api_key_revoke"}

	closed, _ := newAPIKeyServer(t, false)
	names := toolsListNames(t, closed)
	for _, n := range mutating {
		if names[n] {
			t.Errorf("%s changes keys and must not be listed without --allow-mutations", n)
		}
	}
	open, _ := newAPIKeyServer(t, true)
	names = toolsListNames(t, open)
	for _, n := range mutating {
		if !names[n] {
			t.Errorf("%s must be listed with --allow-mutations", n)
		}
	}
}

func TestAPIMutatingToolsAreRefusedByNameWithoutTheFlagAndDoNothing(t *testing.T) {
	s, dbPath := newAPIKeyServer(t, false)
	side := apikeys.NewStore(sideDB(t, dbPath).DB)
	existing, _, err := side.Create(context.Background(), "default", "existing", false)
	if err != nil {
		t.Fatal(err)
	}

	for name, args := range map[string]map[string]any{
		"api_key_create": {"name": "sneaky"},
		"api_key_update": {"id": existing.ID, "name": "renamed", "context": true},
		"api_key_revoke": {"id": existing.ID},
	} {
		text, err := callAPITool(t, s, name, args)
		if err == nil || !strings.Contains(err.Error(), "--allow-mutations") {
			t.Errorf("%s without the flag: %q, %v; want a refusal that names --allow-mutations", name, scrubbed(text), err)
		}
	}

	keys, err := side.List(context.Background(), "default", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Name != "existing" || keys[0].Context || keys[0].RevokedAt != nil {
		t.Errorf("a refused call must change nothing, but the keys are now %+v", keys)
	}
}

func TestAPIMutatingToolAnnotations(t *testing.T) {
	want := map[string]map[string]bool{
		"api_key_create": {"readOnlyHint": false},
		"api_key_update": {"readOnlyHint": false},
		"api_key_revoke": {"readOnlyHint": false, "destructiveHint": true},
	}
	got := map[string]map[string]bool{}
	for _, def := range toolDefinitions(true) {
		if name := def["name"].(string); want[name] != nil {
			got[name], _ = def["annotations"].(map[string]bool)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("annotations\n got %v\nwant %v", got, want)
	}
}

func TestAPIKeyCreateReturnsTheKeyAndStoresOnlyItsHash(t *testing.T) {
	s, dbPath := newAPIKeyServer(t, true)
	k := decodeKey(t, mustCall(t, s, "api_key_create", map[string]any{"name": "app", "context": true}))

	secret := stringField(t, k, "key")
	if !strings.HasPrefix(secret, apikeys.KeyPrefix) || len(secret) != 49 {
		t.Fatalf("the key has %d characters, want 49 and the sk-ma- prefix", len(secret))
	}
	if got, want := sortedFields(k), []string{"context", "created_at", "id", "key", "last_used_at", "name", "prefix", "profile_id", "revoked_at"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the result is %v, want the key's metadata and the key: %v", got, want)
	}
	if id, _ := k["id"].(string); !strings.HasPrefix(id, "key_") || k["profile_id"] != "default" || k["name"] != "app" ||
		k["context"] != true || k["prefix"] != secret[:12] || k["created_at"] == nil || k["last_used_at"] != nil || k["revoked_at"] != nil {
		t.Errorf("metadata %s", describe(k))
	}

	// The key works, as the server's profile.
	side := sideDB(t, dbPath)
	got, err := apikeys.NewStore(side.DB).Authenticate(context.Background(), secret)
	if err != nil || got.ID != k["id"] || got.ProfileID != "default" || !got.Context {
		t.Errorf("the key the tool returned does not authenticate as the profile: %+v, %v", got, err)
	}
	// Only its hash is stored.
	rows, err := side.DB.Query(`SELECT id, profile_id, name, prefix, key_hash FROM api_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, profile, name, prefix, hash string
		if err := rows.Scan(&id, &profile, &name, &prefix, &hash); err != nil {
			t.Fatal(err)
		}
		n++
		if hash != apikeys.HashKey(secret) {
			t.Error("the stored hash is not the SHA-256 of the key")
		}
		for _, col := range []string{id, profile, name, prefix, hash} {
			if strings.Contains(col, secret) {
				t.Error("the key itself was stored")
			}
		}
	}
	if n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

func TestAPIKeyCreateWithoutContextIsAPlainKey(t *testing.T) {
	s, _ := newAPIKeyServer(t, true)
	if k := decodeKey(t, mustCall(t, s, "api_key_create", map[string]any{"name": "plain"})); k["context"] != false {
		t.Errorf("context must default to off: %v", k["context"])
	}
}

// The rules are the store's: the names it refuses, the duplicates it refuses, in its
// own words, and nothing is created.
func TestAPIKeyCreateRefusesWhatTheStoreRefuses(t *testing.T) {
	s, dbPath := newAPIKeyServer(t, true)

	for _, bad := range []any{"", " lead", "-dash", strings.Repeat("a", 65), "semi;colon", "new\nline", "key_isqzhh2a5itg"} {
		if text, err := callAPITool(t, s, "api_key_create", map[string]any{"name": bad}); err == nil || err.Error() != apikeys.ErrInvalidName.Error() {
			t.Errorf("name %q: %q, %v; want the store's invalid-name error", bad, scrubbed(text), err)
		}
	}
	if _, err := callAPITool(t, s, "api_key_create", nil); err == nil || err.Error() != apikeys.ErrInvalidName.Error() {
		t.Errorf("no name at all: %v", err)
	}
	if _, err := callAPITool(t, s, "api_key_create", map[string]any{"name": "ok", "context": "yes"}); err == nil || !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("context must be a boolean: %v", err)
	}
	if n := keyRows(t, dbPath); n != 0 {
		t.Fatalf("a refused create stored %d keys", n)
	}

	createKey(t, s, "app", false)
	if _, err := callAPITool(t, s, "api_key_create", map[string]any{"name": "app"}); err == nil || err.Error() != apikeys.ErrNameTaken.Error() {
		t.Errorf("a duplicate active name: %v, want the store's name-taken error", err)
	}
	// Another profile's key of that name is no clash.
	other := apikeys.NewStore(sideDB(t, dbPath).DB)
	if _, _, err := other.Create(context.Background(), "other-profile", "shared", false); err != nil {
		t.Fatal(err)
	}
	createKey(t, s, "shared", false)
	if n := keyRows(t, dbPath); n != 3 {
		t.Errorf("%d keys, want 3 (app, shared, and the other profile's shared)", n)
	}
}

func TestAPIKeyUpdateRenamesAndSwitchesContext(t *testing.T) {
	s, _ := newAPIKeyServer(t, true)
	created := decodeKey(t, mustCall(t, s, "api_key_create", map[string]any{"name": "app"}))
	id := stringField(t, created, "id")

	k := decodeKey(t, mustCall(t, s, "api_key_update", map[string]any{"id": id, "name": "app-2", "context": true}))
	if k["id"] != id || k["name"] != "app-2" || k["context"] != true {
		t.Errorf("rename and context on: %s", describe(k))
	}
	// By the name of an active key, and an explicit false turns context off.
	k = decodeKey(t, mustCall(t, s, "api_key_update", map[string]any{"id": "app-2", "context": false}))
	if k["context"] != false || k["name"] != "app-2" {
		t.Errorf("context:false must turn it off and leave the name: %s", describe(k))
	}
	// An argument left out is not false: renaming alone keeps the context as it was.
	mustCall(t, s, "api_key_update", map[string]any{"id": id, "context": true})
	k = decodeKey(t, mustCall(t, s, "api_key_update", map[string]any{"id": id, "name": "app-3"}))
	if k["context"] != true || k["name"] != "app-3" {
		t.Errorf("a rename alone must keep context on: %s", describe(k))
	}
	// The key is not part of an update, and not changed by it.
	if _, has := k["key"]; has || k["prefix"] != created["prefix"] || k["created_at"] != created["created_at"] {
		t.Errorf("update returned or changed more than the metadata: %s", describe(k))
	}
}

func TestAPIKeyUpdateRefusals(t *testing.T) {
	s, _ := newAPIKeyServer(t, true)
	one, _ := createKey(t, s, "one", false)
	createKey(t, s, "two", false)
	revoked, _ := createKey(t, s, "gone", false)
	mustCall(t, s, "api_key_revoke", map[string]any{"id": revoked})

	for _, c := range []struct {
		what string
		args map[string]any
		want string
	}{
		{"nothing to change", map[string]any{"id": one}, "nothing to change"},
		{"null arguments are nothing either", map[string]any{"id": one, "name": nil, "context": nil}, "nothing to change"},
		{"no id", map[string]any{"name": "x"}, "id is required"},
		{"a name that is taken", map[string]any{"id": one, "name": "two"}, apikeys.ErrNameTaken.Error()},
		{"an invalid name", map[string]any{"id": one, "name": "bad;name"}, apikeys.ErrInvalidName.Error()},
		{"an explicit empty name is invalid, not absent", map[string]any{"id": one, "name": ""}, apikeys.ErrInvalidName.Error()},
		{"an unknown id", map[string]any{"id": "key_zzzzzzzzzzzz", "context": true}, apikeys.ErrNotFound.Error()},
		{"a revoked key", map[string]any{"id": revoked, "context": true}, apikeys.ErrNotFound.Error()},
		{"a name of the wrong type", map[string]any{"id": one, "name": 5}, "invalid arguments"},
		{"a context of the wrong type", map[string]any{"id": one, "context": "yes"}, "invalid arguments"},
	} {
		if text, err := callAPITool(t, s, "api_key_update", c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %q, %v; want an error containing %q", c.what, scrubbed(text), err, c.want)
		}
	}

	keys := decodeKeys(t, mustCall(t, s, "api_key_list", nil))
	if got := namesOf(keys); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Errorf("the refusals changed something: %v", got)
	}
	for _, k := range keys {
		if k["context"] != false {
			t.Errorf("a refused update changed %s", describe(k))
		}
	}
}

func TestAPIKeyRevokeEndsTheKeyAtOnce(t *testing.T) {
	s, dbPath := newAPIKeyServer(t, true)
	id, secret := createKey(t, s, "app", false)
	store := apikeys.NewStore(sideDB(t, dbPath).DB)
	ctx := context.Background()
	if _, err := store.Authenticate(ctx, secret); err != nil {
		t.Fatalf("a new key must work: %v", err)
	}

	k := decodeKey(t, mustCall(t, s, "api_key_revoke", map[string]any{"id": id}))
	if k["id"] != id || k["revoked_at"] == nil {
		t.Errorf("revoke: %s", describe(k))
	}
	if _, has := k["key"]; has {
		t.Error("a revoke result must not carry the key")
	}
	if _, err := store.Authenticate(ctx, secret); err == nil {
		t.Error("a revoked key still authenticates")
	}

	// Revoking by id again succeeds and leaves it as it was.
	again := decodeKey(t, mustCall(t, s, "api_key_revoke", map[string]any{"id": id}))
	if again["revoked_at"] != k["revoked_at"] {
		t.Errorf("a second revoke moved revoked_at: %v then %v", k["revoked_at"], again["revoked_at"])
	}
	// By name it is not found afterwards, because a name belongs to active keys; the
	// name is free again.
	if _, err := callAPITool(t, s, "api_key_revoke", map[string]any{"id": "app"}); err == nil || err.Error() != apikeys.ErrNotFound.Error() {
		t.Errorf("revoking a revoked key by name: %v", err)
	}
	createKey(t, s, "app", false)
	if got := namesOf(decodeKeys(t, mustCall(t, s, "api_key_list", nil))); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("active keys %v", got)
	}
	if n := len(decodeKeys(t, mustCall(t, s, "api_key_list", map[string]any{"include_revoked": true}))); n != 2 {
		t.Errorf("include_revoked lists %d keys, want the revoked one and the new one", n)
	}
}

func TestAPIKeyRevokeRefusals(t *testing.T) {
	s, _ := newAPIKeyServer(t, true)
	if _, err := callAPITool(t, s, "api_key_revoke", nil); err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Errorf("no id: %v", err)
	}
	if _, err := callAPITool(t, s, "api_key_revoke", map[string]any{"id": "key_zzzzzzzzzzzz"}); err == nil || err.Error() != apikeys.ErrNotFound.Error() {
		t.Errorf("an unknown id: %v", err)
	}
}

// Another profile's key is "not found", worded exactly as an id nobody has: not
// "forbidden", nothing that tells a guess from a hit. It is also left as it was.
func TestAPIKeysOfAnotherProfileAreNotFound(t *testing.T) {
	s, dbPath := newAPIKeyServer(t, true)
	ctx := context.Background()
	side := apikeys.NewStore(sideDB(t, dbPath).DB)
	theirs, theirSecret, err := side.Create(ctx, "other-profile", "theirs", false)
	if err != nil {
		t.Fatal(err)
	}

	_, unknown := callAPITool(t, s, "api_key_revoke", map[string]any{"id": "key_zzzzzzzzzzzz"})
	if unknown == nil {
		t.Fatal("an unknown id must be an error")
	}
	for _, ref := range []string{theirs.ID, "theirs"} {
		for name, args := range map[string]map[string]any{
			"api_key_update": {"id": ref, "name": "mine", "context": true},
			"api_key_revoke": {"id": ref},
		} {
			_, err := callAPITool(t, s, name, args)
			if err == nil || err.Error() != unknown.Error() {
				t.Errorf("%s of another profile's key by %q: %v, want exactly %q", name, ref, err, unknown)
			}
		}
	}

	left, err := side.Get(ctx, "other-profile", theirs.ID)
	if err != nil || left.Name != "theirs" || left.Context || left.RevokedAt != nil {
		t.Errorf("another profile's key was touched: %+v, %v", left, err)
	}
	if _, err := side.Authenticate(ctx, theirSecret); err != nil {
		t.Errorf("another profile's key stopped working: %v", err)
	}
	// Their name is no clash for this profile.
	createKey(t, s, "theirs", false)
}
