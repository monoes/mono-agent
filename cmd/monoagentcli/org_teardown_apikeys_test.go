package main

import (
	"context"
	"errors"
	"testing"

	"github.com/monoes/mono-agent/internal/apikeys"
)

// Deleting a profile must not leave API keys that still authenticate as it:
// `org teardown-profile` revokes them with the org grants and endpoints.
func TestOrgTeardownProfileRevokesAPIKeys(t *testing.T) {
	f := newOrgCLIFixture(t)
	db, profileID, _, err := (&orgEnv{cfg: f.cfg}).Profile()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := apikeys.NewStore(db.DB)
	_, secret, err := store.Create(ctx, profileID, "svc", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create(ctx, profileID, "svc-2", false); err != nil {
		t.Fatal(err)
	}
	_, otherSecret, err := store.Create(ctx, "another-profile", "svc", false)
	if err != nil {
		t.Fatal(err)
	}

	preview := f.mustRun(t, "teardown-profile", "--dry-run")
	if preview["api_keys"] != float64(2) {
		t.Fatalf("dry-run api_keys = %v, want 2", preview["api_keys"])
	}
	if _, err := store.Authenticate(ctx, secret); err != nil {
		t.Fatalf("a dry run revoked a key: %v", err)
	}

	out := f.mustRun(t, "teardown-profile")
	if out["api_keys"] != float64(2) {
		t.Fatalf("api_keys = %v, want 2", out["api_keys"])
	}
	if _, err := store.Authenticate(ctx, secret); !errors.Is(err, apikeys.ErrInvalidKey) {
		t.Fatalf("a key of the torn-down profile still authenticates: %v", err)
	}
	if _, err := store.Authenticate(ctx, otherSecret); err != nil {
		t.Fatalf("another profile's key must survive: %v", err)
	}
}
