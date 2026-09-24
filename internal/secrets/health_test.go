package secrets

import (
	"context"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestCheckVault(t *testing.T) {
	keyring.MockInit()
	db := newDEKTestDB(t)
	ctx := context.Background()

	if st, err := CheckVault(ctx, db.DB, "p1"); st != VaultUninitialized || err != nil {
		t.Fatalf("fresh: got %q, %v; want uninitialized", st, err)
	}

	if _, err := getOrCreateDEK(ctx, db.DB, "p1"); err != nil {
		t.Fatal(err)
	}
	if st, err := CheckVault(ctx, db.DB, "p1"); st != VaultOK || err != nil {
		t.Fatalf("bootstrapped: got %q, %v; want ok", st, err)
	}

	// A different KEK in the keychain no longer unwraps the stored DEK.
	if err := keyring.Set(keyringService, kekAccount("p1"), "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"); err != nil {
		t.Fatal(err)
	}
	if st, err := CheckVault(ctx, db.DB, "p1"); st != VaultKeyMismatch || err == nil {
		t.Fatalf("wrong KEK: got %q, %v; want key-mismatch", st, err)
	}

	if err := keyring.Delete(keyringService, kekAccount("p1")); err != nil {
		t.Fatal(err)
	}
	if st, err := CheckVault(ctx, db.DB, "p1"); st != VaultKeyMissing || err == nil {
		t.Fatalf("no KEK: got %q, %v; want key-missing", st, err)
	}
}
