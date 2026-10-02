package mcp

// The other tests of these tools run as the default profile, whose id is also what a
// handler that forgot the server's profile and wrote "default" would use: they
// cannot tell a scoped tool from an unscoped one. These run as a profile whose id is
// neither "default" nor its own name, as the id of a profile made by `profile create`
// or the desktop app is, and the server is opened by that name, so that finding the
// id is part of what is tested.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

const (
	workProfileID   = "p-7f3a9c"
	workProfileName = "Work"
)

// workServer is a server, with mutations allowed, of the profile workProfileName,
// and a second handle on its database.
func workServer(t *testing.T) (*Server, *storage.Database) {
	t.Helper()
	dbPath := testdb.Path(t)
	side := sideDB(t, dbPath)
	if _, err := side.DB.Exec(`INSERT INTO profiles (id, name) VALUES (?, ?)`, workProfileID, workProfileName); err != nil {
		t.Fatal(err)
	}
	return serverOver(t, dbPath, workProfileName, true), side
}

func TestAPIKeyToolsBelongToTheServersProfileByItsID(t *testing.T) {
	s, side := workServer(t)
	ctx := context.Background()
	store := apikeys.NewStore(side.DB)
	theirs, theirSecret, err := store.Create(ctx, "default", "theirs", false)
	if err != nil {
		t.Fatal(err)
	}

	// A key made through the tool belongs to the profile by its id: not the name it was
	// opened by, and not "default".
	created := decodeKey(t, mustCall(t, s, "api_key_create", map[string]any{"name": "mine", "context": true}))
	if got := stringField(t, created, "profile_id"); got != workProfileID {
		t.Errorf("a key created by the server of profile %s belongs to %q", workProfileID, got)
	}
	for profile, want := range map[string][]string{workProfileID: {"mine"}, "default": {"theirs"}, workProfileName: {}} {
		keys, err := store.List(ctx, profile, true)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, k := range keys {
			got = append(got, k.Name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("keys stored for profile %q = %v, want %v", profile, got, want)
		}
	}
	if got := namesOf(decodeKeys(t, mustCall(t, s, "api_key_list", map[string]any{"include_revoked": true}))); !reflect.DeepEqual(got, []string{"mine"}) {
		t.Errorf("api_key_list = %v, want only this profile's [mine]", got)
	}

	// The default profile's key is not found, by id and by name, worded as an id nobody
	// has, and it is left as it was.
	_, unknown := callAPITool(t, s, "api_key_revoke", map[string]any{"id": "key_zzzzzzzzzzzz"})
	if unknown == nil {
		t.Fatal("an unknown id must be an error")
	}
	for _, ref := range []string{theirs.ID, "theirs"} {
		for name, args := range map[string]map[string]any{
			"api_key_update": {"id": ref, "name": "taken", "context": true},
			"api_key_revoke": {"id": ref},
		} {
			if _, err := callAPITool(t, s, name, args); err == nil || err.Error() != unknown.Error() {
				t.Errorf("%s of the default profile's key by %q: %v, want exactly %q", name, ref, err, unknown)
			}
		}
	}
	assertUntouched := func(when string) {
		t.Helper()
		left, err := store.Get(ctx, "default", theirs.ID)
		if err != nil || left.Name != "theirs" || left.Context || left.RevokedAt != nil {
			t.Errorf("%s the default profile's key was touched: %+v, %v", when, left, err)
		}
		if _, err := store.Authenticate(ctx, theirSecret); err != nil {
			t.Errorf("%s the default profile's key stopped working: %v", when, err)
		}
	}
	assertUntouched("after refused calls,")

	// A name means this profile's active key of that name: the same name in both
	// profiles is no clash, and a call by it reaches this profile's key only.
	mine, _ := createKey(t, s, "theirs", false)
	mustCall(t, s, "api_key_update", map[string]any{"id": "theirs", "context": true})
	if k, err := store.Get(ctx, workProfileID, mine); err != nil || !k.Context {
		t.Errorf("an update by name did not reach this profile's key: %+v, %v", k, err)
	}
	mustCall(t, s, "api_key_revoke", map[string]any{"id": "theirs"})
	if k, err := store.Get(ctx, workProfileID, mine); err != nil || k.RevokedAt == nil {
		t.Errorf("a revoke by name did not reach this profile's key: %+v, %v", k, err)
	}
	assertUntouched("after calls by the same name,")
}

// Whether auto works is read from the Jev settings of the profile by its id: the
// surface switched on for the default profile does nothing for the work profile.
func TestAPIModelsListReadsAutoOfTheServersProfileByItsID(t *testing.T) {
	pinAPIEnv(t)
	fakeAPIMonomind(t)
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	s, side := workServer(t)

	if err := jevconf.SetEnabled(side.DB, "default", jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}
	if r := modelsReport(t, s, nil); r.Auto.Available || !strings.Contains(r.Auto.Missing, "jev enable api_auto") {
		t.Errorf("the surface is on for the default profile only: %+v", r.Auto)
	}

	if err := jevconf.SetEnabled(side.DB, workProfileID, jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}
	if r := modelsReport(t, s, nil); !r.Auto.Available || r.Auto.KeySource != "env" {
		t.Errorf("the surface is on for the work profile, and a key is in the environment: %+v", r.Auto)
	}
}
